// Command server runs the worker-control-ui backend: it serves the static
// control panel and the HTTP API that starts/stops the per-cluster worker,
// dispatches per-cluster runner Jobs, and streams their logs.
package main

import (
	"io/fs"
	"log"
	"net/http"

	"github.com/bitovi/temporal-self-hosting-workshop/worker-control-ui/internal/api"
	"github.com/bitovi/temporal-self-hosting-workshop/worker-control-ui/internal/k8s"
	"github.com/bitovi/temporal-self-hosting-workshop/worker-control-ui/internal/webassets"
)

func main() {
	manager, err := k8s.NewManager()
	if err != nil {
		log.Fatalf("building kubernetes manager: %v", err)
	}

	static, err := fs.Sub(webassets.FS, "web")
	if err != nil {
		log.Fatalf("mounting embedded web assets: %v", err)
	}

	mux := api.New(manager).Routes()
	// http.FileServer resolves "/" to "index.html" on its own; serving the
	// whole embedded FS at the root (rather than under a "/static/" prefix)
	// avoids its built-in redirect-to-"./" special case for literal
	// ".../index.html" requests.
	mux.Handle("/", http.FileServer(http.FS(static)))

	addr := ":8090"
	log.Printf("worker-control-ui listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
