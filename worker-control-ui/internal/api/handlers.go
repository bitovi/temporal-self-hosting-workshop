// Package api exposes the worker-control-ui backend as HTTP endpoints:
// start/stop/kill/status for the per-cluster worker and runner, and
// Server-Sent-Events log streams for both. All it does is validate the
// {cluster} path parameter and delegate to internal/k8s.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/bitovi/temporal-self-hosting-workshop/worker-control-ui/internal/k8s"
)

type Handlers struct {
	manager *k8s.Manager
}

func New(manager *k8s.Manager) *Handlers {
	return &Handlers{manager: manager}
}

// target bundles the k8s.Manager operations for one workload kind (worker or
// runner) so stop/kill/status/logs can be wired up identically for both
// instead of duplicating each handler twice. Start is handled separately per
// kind: Worker's takes no config, Runner's takes a RunnerConfig body.
type target struct {
	stop   func(context.Context, k8s.Cluster) error
	kill   func(context.Context, k8s.Cluster) error
	status func(context.Context, k8s.Cluster) (k8s.RunState, error)
	logs   func(context.Context, k8s.Cluster, io.Writer) error
}

// Routes returns a ServeMux with every endpoint registered.
func (h *Handlers) Routes() *http.ServeMux {
	worker := target{
		stop:   h.manager.StopWorker,
		kill:   h.manager.KillWorker,
		status: h.manager.WorkerStatus,
		logs:   h.manager.StreamWorkerLogs,
	}
	runner := target{
		stop:   h.manager.StopRunner,
		kill:   h.manager.KillRunner,
		status: h.manager.RunnerStatus,
		logs:   h.manager.StreamRunnerLogs,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/worker/{cluster}/start", h.startWorker)
	mux.HandleFunc("POST /api/runner/{cluster}/start", h.startRunner)

	for _, kind := range []struct {
		path string
		t    target
	}{
		{"worker", worker},
		{"runner", runner},
	} {
		t := kind.t
		mux.HandleFunc("POST /api/"+kind.path+"/{cluster}/stop", stop(t))
		mux.HandleFunc("POST /api/"+kind.path+"/{cluster}/kill", kill(t))
		mux.HandleFunc("GET /api/"+kind.path+"/{cluster}/status", status(t))
		mux.HandleFunc("GET /api/logs/"+kind.path+"/{cluster}", h.streamLogs(t))
	}

	return mux
}

// cluster extracts and validates the {cluster} path parameter, writing a 400
// response and returning ok=false if it isn't one this service knows about.
func cluster(w http.ResponseWriter, r *http.Request) (k8s.Cluster, bool) {
	c := r.PathValue("cluster")
	if !k8s.ValidCluster(c) {
		http.Error(w, fmt.Sprintf("unknown cluster %q (want cluster-1 or cluster-2)", c), http.StatusBadRequest)
		return "", false
	}
	return k8s.Cluster(c), true
}

func writeState(w http.ResponseWriter, state k8s.RunState) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"state": string(state)})
}

// startWorker decodes the Worker's instance count from the request body and
// starts it. A missing or zero replicas field defaults to 1, keeping a
// bodyless request (the shape used before Worker had a configurable instance
// count) equivalent to today's single-replica behavior.
func (h *Handlers) startWorker(w http.ResponseWriter, r *http.Request) {
	c, ok := cluster(w, r)
	if !ok {
		return
	}

	var body struct {
		Replicas int32 `json:"replicas"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("decoding worker config: %v", err), http.StatusBadRequest)
			return
		}
	}
	if body.Replicas == 0 {
		body.Replicas = 1
	}

	if err := h.manager.StartWorker(r.Context(), c, body.Replicas); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeState(w, k8s.StateStarting)
}

// startRunner decodes the Runner's mode/depth/example/rate config from the
// request body and starts it. Unlike Worker's start (no config), the Runner
// needs this per-request body to pick between steady-load and rate-limited
// dispatch (see k8s.RunnerConfig).
func (h *Handlers) startRunner(w http.ResponseWriter, r *http.Request) {
	c, ok := cluster(w, r)
	if !ok {
		return
	}

	var cfg k8s.RunnerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, fmt.Sprintf("decoding runner config: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.manager.StartRunner(r.Context(), c, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeState(w, k8s.StateStarting)
}

func stop(t target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := cluster(w, r)
		if !ok {
			return
		}
		if err := t.stop(r.Context(), c); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeState(w, k8s.StateStopped)
	}
}

// kill force-restarts the workload's pod (grace period 0) without changing
// whether it's toggled on -- useful for exercises that demonstrate
// crash/restart behavior rather than a clean shutdown.
func kill(t target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := cluster(w, r)
		if !ok {
			return
		}
		if err := t.kill(r.Context(), c); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeState(w, k8s.StateStarting)
	}
}

func status(t target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := cluster(w, r)
		if !ok {
			return
		}
		state, err := t.status(r.Context(), c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeState(w, state)
	}
}

func (h *Handlers) streamLogs(t target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := cluster(w, r)
		if !ok {
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		sw := &sseWriter{w: w, flusher: flusher}
		if err := t.logs(r.Context(), c, sw); err != nil {
			log.Printf("log stream ended with error: %v", err)
			sw.writeEvent("error", err.Error())
		}
	}
}

// sseWriter adapts an http.ResponseWriter into the io.Writer the k8s package
// streams log lines into, formatting each line as an SSE "data:" message and
// flushing immediately so the browser sees it without buffering delay.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", line); err != nil {
		return 0, err
	}
	s.flusher.Flush()
	return len(p), nil
}

func (s *sseWriter) Flush() { s.flusher.Flush() }

func (s *sseWriter) writeEvent(event, data string) {
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	s.flusher.Flush()
}
