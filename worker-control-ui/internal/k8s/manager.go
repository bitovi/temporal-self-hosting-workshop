// Package k8s wraps the Kubernetes API calls the worker-control-ui backend
// needs: starting/stopping the per-cluster worker and runner Deployments,
// killing their processes in place (for workshop exercises that demonstrate
// crash/restart behavior), and streaming their logs. It never talks to
// Temporal directly -- every workload it manages runs the benchmark-workers
// binaries, which is what actually holds a Temporal client.
package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	// Kubernetes namespace everything in this workshop runs in. Not to be
	// confused with temporalNamespace below -- a completely different
	// "namespace" (Temporal's), just an unfortunate name collision.
	namespace = "default"

	// temporalNamespace is the Temporal namespace the worker and runner
	// register/dispatch against. Must exist on both clusters already --
	// see start.sh's ensure_temporal_namespace.
	temporalNamespace = "workshop"

	// runtimeImage carries the same statically-linked worker/runner binaries
	// benchmark-workers/deployment.yaml uses, but on a busybox base instead
	// of the upstream image's `scratch` base -- see worker-control-ui/runtime/
	// Dockerfile. A shell is what lets Manager.kill exec into the pod and
	// signal the process directly instead of deleting the pod.
	runtimeImage = "worker-control-ui-runtime:local"

	// taskQueue is shared by the worker and runner: both need to agree on
	// it for the hello-world job to actually get picked up.
	taskQueue = "hello-world"

	// omesRateModeMaxConcurrent stands in for "no queue ceiling" in Rate
	// mode: omes' --max-concurrent treats 0 as "use its own default of 10"
	// rather than "unlimited" (confirmed from its source), so a large
	// explicit number is the only way to get an effectively uncapped
	// concurrency while --max-iterations-per-second does the real limiting.
	omesRateModeMaxConcurrent = "100000"

	appLabel     = "app"
	clusterLabel = "cluster"
	workerKind   = "worker-control-worker"
	runnerKind   = "worker-control-runner"
)

// Cluster identifies one of the two Temporal clusters this workshop runs.
type Cluster string

const (
	Cluster1 Cluster = "cluster-1"
	Cluster2 Cluster = "cluster-2"
)

// ValidCluster reports whether c is a cluster this service knows how to
// target.
func ValidCluster(c string) bool {
	return c == string(Cluster1) || c == string(Cluster2)
}

// frontendPort maps each cluster to its frontend Service's exposed port.
// cluster-1's Service listens on 7233; cluster-2's listens on 8233 (both
// forward to the pod's container port 7233) -- see helm/cluster-*-temporal-values.yaml's
// frontend.service.port.
var frontendPort = map[Cluster]string{
	Cluster1: "7233",
	Cluster2: "8233",
}

func (c Cluster) frontendAddr() string {
	return fmt.Sprintf("%s-temporal-frontend:%s", c, frontendPort[c])
}

// RunState is the lifecycle state reported for the worker or runner.
type RunState string

const (
	StateStopped  RunState = "stopped"
	StateStarting RunState = "starting"
	StateRunning  RunState = "running"
)

// RunnerConfig selects the Runner's dispatch mode and its knobs. Mode is
// either "steady" (benchmark-workers' runner, keeping Depth executions
// continuously in flight) or "rate" (omes, dispatching at a fixed Rate per
// second with no concurrency ceiling). Rate mode further picks a Scenario:
// our own hello-world dispatcher (against the existing Worker), or one of
// two built-in omes kitchen-sink scenarios (each brings its own worker).
type RunnerConfig struct {
	Mode    string
	Depth   int // steady mode: how many executions to keep in flight (-c)
	Example int // steady mode, and rate mode's "hello_world" scenario

	Rate     float64 // rate mode: target dispatch rate, in requests/second
	Scenario string  // rate mode: "hello_world" | "many_timers" | "fan_out"

	// Rate mode's "many_timers" scenario. Zero means "let omes use its own
	// default" -- the flag is simply omitted rather than passing a zero.
	ConcurrentTimers     int
	TimerDurationSeconds int

	// Rate mode's "fan_out" scenario. Same zero-means-omit convention.
	ChildrenPerWorkflow   int
	ActivitiesPerWorkflow int
}

const (
	RunnerModeSteady = "steady"
	RunnerModeRate   = "rate"
)

const (
	RunnerScenarioHelloWorld = "hello_world"
	RunnerScenarioManyTimers = "many_timers"
	RunnerScenarioFanOut     = "fan_out"
)

// runnerExample maps the 3 workshop example workflows to the benchmark-workers
// workflow type + input that implements each -- all already exist in
// benchmark-workers/workflows/workflow.go, no new Temporal code needed.
type runnerExample struct {
	workflowType string
	input        string
}

var runnerExamples = map[int]runnerExample{
	// Hello world, no activity at all.
	1: {"ExecuteActivityWorkflow", `{"Count":0}`},
	// Hello world via a single activity call (the original default job).
	2: {"ExecuteActivityWorkflow", `{"Count":1,"Activity":"Echo","Input":{"Message":"Hello, World!"}}`},
	// Hello world via an activity, then a 5s timer. DSLWorkflow sleeps
	// *before* an activity within the same step, so the activity and the
	// sleep need to be two separate steps to get "activity, then sleep".
	3: {"DSLWorkflow", `[{"a":"Echo","i":{"Message":"Hello, World!"}},{"t":5}]`},
}

type Manager struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
}

// NewManager builds a Manager using the in-cluster ServiceAccount config.
// Falls back to the caller's kubeconfig (via KUBECONFIG / ~/.kube/config)
// when not running inside a pod, so the binary is also runnable directly
// against the devcontainer's k3d cluster while developing it.
func NewManager() (*Manager, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("no in-cluster config and no kubeconfig available: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes clientset: %w", err)
	}

	return &Manager{clientset: clientset, restConfig: cfg}, nil
}

// wrappedCommand runs bin (with args) as a *child* of a tiny shell wrapper
// instead of directly as the container's PID 1. This matters for Kill: Linux
// gives PID 1 of a PID namespace special signal-handling semantics -- even
// SIGKILL sent to it from a process inside the same namespace (which is what
// an `exec` session always is) is silently ignored (confirmed live: `kubectl
// exec ... kill -9 1` had no effect on the running worker process at all).
// Running the real binary as PID 1's child sidesteps that entirely: killing
// it is a normal, unprotected signal delivery, and `wait`/`exit $?` makes the
// wrapper exit right after, so the container still crashes and (if the
// Deployment still wants a replica) kubelet still restarts it in place.
// Passing bin/args as separate argv elements via "$0"/"$@", rather than
// interpolating them into the script text, avoids needing to shell-escape
// the runner's JSON workflow input.
func wrappedCommand(bin string, args ...string) []string {
	cmd := []string{"sh", "-c", `exec "$0" "$@" & wait $!; exit $?`, bin}
	return append(cmd, args...)
}

func deploymentName(kind string, cluster Cluster) string {
	return fmt.Sprintf("%s-%s", kind, cluster)
}

func labelsFor(kind string, cluster Cluster) map[string]string {
	return map[string]string{appLabel: kind, clusterLabel: string(cluster)}
}

func selectorFor(kind string, cluster Cluster) string {
	return fmt.Sprintf("%s=%s,%s=%s", appLabel, kind, clusterLabel, cluster)
}

// start ensures a Deployment matching wantSpec is running, creating it on
// first use. On an existing Deployment it overwrites the whole Spec (not
// just Replicas) so that a reconfigured Runner (different mode/depth/
// example/rate) actually takes effect on the next Start instead of silently
// keeping whatever container command was there before -- the Worker's own
// spec never varies between starts, so this is a no-op change for it.
func (m *Manager) start(ctx context.Context, name string, wantSpec *appsv1.Deployment) error {
	deployments := m.clientset.AppsV1().Deployments(namespace)

	existing, err := deployments.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := deployments.Create(ctx, wantSpec, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("getting deployment %s: %w", name, err)
	}

	existing.Spec = wantSpec.Spec
	_, err = deployments.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// stop scales name's Deployment to zero replicas. A Deployment that was
// never started is already "stopped", so a missing Deployment is not an
// error.
func (m *Manager) stop(ctx context.Context, name string) error {
	deployments := m.clientset.AppsV1().Deployments(namespace)

	existing, err := deployments.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting deployment %s: %w", name, err)
	}

	zero := int32(0)
	existing.Spec.Replicas = &zero
	_, err = deployments.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// status reports the current lifecycle state of name's Deployment.
func (m *Manager) status(ctx context.Context, name string) (RunState, error) {
	dep, err := m.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StateStopped, nil
	}
	if err != nil {
		return "", fmt.Errorf("getting deployment %s: %w", name, err)
	}

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas == 0 {
		return StateStopped, nil
	}
	if dep.Status.ReadyReplicas > 0 {
		return StateRunning, nil
	}
	return StateStarting, nil
}

// kill sends SIGKILL to the worker/runner process of the pod(s) matching
// selector, without deleting the Pod itself: kubelet restarts just the
// container in place (the Deployment's restartPolicy is Always), so the
// Pod's identity and its crashed container's logs stay reachable via
// `kubectl logs --previous` instead of vanishing along with a deleted Pod.
// Useful for workshop exercises that demonstrate what happens when a worker
// or runner crashes rather than shuts down cleanly.
func (m *Manager) kill(ctx context.Context, selector, containerName string) error {
	pods := m.clientset.CoreV1().Pods(namespace)
	list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("listing pods for %q: %w", selector, err)
	}

	for _, pod := range list.Items {
		if err := m.signalContainer(ctx, pod.Name, containerName); err != nil {
			return err
		}
	}
	return nil
}

// signalContainer execs a SIGKILL inside podName's containerName, targeting
// every process it can see *except* PID 1 (`kill -9 -1`, standard kill(2)
// semantics for a -1 pid), terminating the worker/runner binary without
// touching the Pod object. It deliberately doesn't target PID 1 directly:
// the container's command (see wrappedCommand) runs the real binary as PID
// 1's child specifically because Linux makes PID 1 of a PID namespace immune
// to SIGKILL sent from inside that same namespace -- confirmed live, `kill
// -9 1` from an exec session had no effect at all on a plain worker process.
func (m *Manager) signalContainer(ctx context.Context, podName, containerName string) error {
	req := m.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   []string{"kill", "-9", "-1"},
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(m.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("building exec for pod %s: %w", podName, err)
	}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	// The exec'd process is PID 1 killing itself -- the connection is
	// expected to be severed by that, not to return a clean exit status.
	if err != nil {
		return fmt.Errorf("signaling pod %s container %s: %w", podName, containerName, err)
	}
	return nil
}

func workerDeployment(cluster Cluster, replicas int32) *appsv1.Deployment {
	name := deploymentName(workerKind, cluster)
	labels := labelsFor(workerKind, cluster)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "worker",
							Image:   runtimeImage,
							Command: wrappedCommand("/usr/local/bin/worker"),
							Env: []corev1.EnvVar{
								{Name: "TEMPORAL_GRPC_ENDPOINT", Value: cluster.frontendAddr()},
								{Name: "TEMPORAL_NAMESPACE", Value: temporalNamespace},
								{Name: "TEMPORAL_TASK_QUEUE", Value: taskQueue},
							},
						},
					},
				},
			},
		},
	}
}

// rateModeArgs builds the omes CLI args for Rate mode's chosen scenario.
// All three scenarios share the rate/concurrency/lifecycle flags; they
// differ in --scenario, whether they need omes' own prebuilt worker
// (run-scenario-with-worker --dir-name wcui, for the two built-in
// kitchen-sink scenarios) or dispatch against the existing benchmark-workers
// Worker instead (run-scenario, for our own hello-world scenario), and their
// own handful of --option flags (only passed when nonzero, letting omes'
// own default apply otherwise).
func rateModeArgs(cluster Cluster, cfg RunnerConfig) ([]string, error) {
	common := []string{
		"--server-address", cluster.frontendAddr(),
		"--namespace", temporalNamespace,
		"--run-id", fmt.Sprintf("wcui-%s", cluster),
		// workflow_with_many_actions reuses the same child-workflow IDs
		// across iterations (they're derived from --run-id, not per-
		// iteration) -- confirmed live, without this flag the 2nd iteration
		// onward fails with "child workflow execution already started".
		// Harmless for the other two scenarios, which don't collide.
		"--ignore-already-started",
		"--max-iterations-per-second", strconv.FormatFloat(cfg.Rate, 'f', -1, 64),
		"--max-concurrent", omesRateModeMaxConcurrent,
		"--duration", "87600h", // ~10 years: the Deployment's own start/stop/kill is the real lifecycle control
	}

	switch cfg.Scenario {
	case "", RunnerScenarioHelloWorld:
		if _, ok := runnerExamples[cfg.Example]; !ok {
			return nil, fmt.Errorf("unknown example %d (want 1, 2, or 3)", cfg.Example)
		}
		args := []string{
			"run-scenario",
			"--scenario", "dispatch_hello_world",
			// Unlike the two built-ins below, this scenario doesn't use
			// omes' kitchen-sink workflow at all, so it needs none of the
			// search attributes that registering would add.
			"--do-not-register-search-attributes",
			"--option", fmt.Sprintf("example=%d", cfg.Example),
		}
		return append(args, common...), nil

	case RunnerScenarioManyTimers:
		args := []string{
			"run-scenario-with-worker",
			"--language", "go", "--dir-name", "wcui",
			"--scenario", "workflow_with_many_timers",
		}
		if cfg.ConcurrentTimers > 0 {
			args = append(args, "--option", fmt.Sprintf("concurrent-timers=%d", cfg.ConcurrentTimers))
		}
		if cfg.TimerDurationSeconds > 0 {
			args = append(args, "--option", fmt.Sprintf("timer-duration=%ds", cfg.TimerDurationSeconds))
		}
		return append(args, common...), nil

	case RunnerScenarioFanOut:
		args := []string{
			"run-scenario-with-worker",
			"--language", "go", "--dir-name", "wcui",
			"--scenario", "workflow_with_many_actions",
		}
		if cfg.ChildrenPerWorkflow > 0 {
			args = append(args, "--option", fmt.Sprintf("children-per-workflow=%d", cfg.ChildrenPerWorkflow))
		}
		if cfg.ActivitiesPerWorkflow > 0 {
			args = append(args, "--option", fmt.Sprintf("activities-per-workflow=%d", cfg.ActivitiesPerWorkflow))
		}
		return append(args, common...), nil

	default:
		return nil, fmt.Errorf("unknown rate-mode scenario %q (want %q, %q, or %q)",
			cfg.Scenario, RunnerScenarioHelloWorld, RunnerScenarioManyTimers, RunnerScenarioFanOut)
	}
}

// runnerDeployment builds the Runner Deployment for cluster according to
// cfg.Mode:
//
//   - "steady" runs benchmark-workers' runner tool, keeping cfg.Depth
//     executions of the chosen cfg.Example continuously in flight (its -c
//     flag bounds the pool's concurrency, refilling immediately as each
//     completes -- this genuinely is "keep N on the queue", confirmed live).
//   - "rate" runs omes at cfg.Rate iterations/second with no concurrency
//     ceiling -- benchmark-workers' runner has no rate-limiting flag at all,
//     confirmed from its source, which is why this mode exists. See
//     rateModeArgs for which of the 3 scenarios/tool invocation applies.
//
// Both modes run their binary via wrappedCommand and under the same
// container name ("runner"), so Kill/logs work identically regardless of
// mode.
func runnerDeployment(cluster Cluster, cfg RunnerConfig) (*appsv1.Deployment, error) {
	one := int32(1)
	name := deploymentName(runnerKind, cluster)
	labels := labelsFor(runnerKind, cluster)

	var command []string
	var env []corev1.EnvVar
	switch cfg.Mode {
	case RunnerModeSteady:
		example, ok := runnerExamples[cfg.Example]
		if !ok {
			return nil, fmt.Errorf("unknown example %d (want 1, 2, or 3)", cfg.Example)
		}
		if cfg.Depth < 1 {
			return nil, fmt.Errorf("queue depth must be at least 1, got %d", cfg.Depth)
		}
		command = wrappedCommand("/usr/local/bin/runner", "-w", "-c", strconv.Itoa(cfg.Depth),
			"-tq", taskQueue,
			"-n", temporalNamespace,
			"-t", example.workflowType,
			example.input,
		)
		// Only the benchmark-workers runner reads this env var -- omes takes
		// the server address as a CLI flag instead (see RunnerModeRate below).
		env = []corev1.EnvVar{{Name: "TEMPORAL_GRPC_ENDPOINT", Value: cluster.frontendAddr()}}
	case RunnerModeRate:
		if cfg.Rate <= 0 {
			return nil, fmt.Errorf("rate must be greater than 0, got %v", cfg.Rate)
		}
		args, err := rateModeArgs(cluster, cfg)
		if err != nil {
			return nil, err
		}
		command = wrappedCommand("/usr/local/bin/omes", args...)
	default:
		return nil, fmt.Errorf("unknown runner mode %q (want %q or %q)", cfg.Mode, RunnerModeSteady, RunnerModeRate)
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "runner",
							Image:   runtimeImage,
							Command: command,
							Env:     env,
						},
					},
				},
			},
		},
	}, nil
}

func (m *Manager) StartWorker(ctx context.Context, cluster Cluster, replicas int32) error {
	if replicas < 1 {
		return fmt.Errorf("instances must be at least 1, got %d", replicas)
	}
	return m.start(ctx, deploymentName(workerKind, cluster), workerDeployment(cluster, replicas))
}

func (m *Manager) StopWorker(ctx context.Context, cluster Cluster) error {
	return m.stop(ctx, deploymentName(workerKind, cluster))
}

func (m *Manager) WorkerStatus(ctx context.Context, cluster Cluster) (RunState, error) {
	return m.status(ctx, deploymentName(workerKind, cluster))
}

// KillWorker sends SIGKILL to cluster's worker process in place (the Pod
// itself is left alone, so its logs stay reachable). Kubelet restarts the
// container immediately since the worker is still toggled on, simulating a
// crash/restart.
func (m *Manager) KillWorker(ctx context.Context, cluster Cluster) error {
	return m.kill(ctx, selectorFor(workerKind, cluster), "worker")
}

func (m *Manager) StartRunner(ctx context.Context, cluster Cluster, cfg RunnerConfig) error {
	deployment, err := runnerDeployment(cluster, cfg)
	if err != nil {
		return err
	}
	return m.start(ctx, deploymentName(runnerKind, cluster), deployment)
}

func (m *Manager) StopRunner(ctx context.Context, cluster Cluster) error {
	return m.stop(ctx, deploymentName(runnerKind, cluster))
}

func (m *Manager) RunnerStatus(ctx context.Context, cluster Cluster) (RunState, error) {
	return m.status(ctx, deploymentName(runnerKind, cluster))
}

// KillRunner sends SIGKILL to cluster's runner process in place (the Pod
// itself is left alone, so its logs stay reachable). Kubelet restarts the
// container immediately since the runner is still toggled on, simulating a
// crash/restart.
func (m *Manager) KillRunner(ctx context.Context, cluster Cluster) error {
	return m.kill(ctx, selectorFor(runnerKind, cluster), "runner")
}

// StreamWorkerLogs follows the log output of cluster's worker pod, writing
// each line to w until the pod's stream ends or ctx is cancelled.
func (m *Manager) StreamWorkerLogs(ctx context.Context, cluster Cluster, w io.Writer) error {
	return m.streamPodLogs(ctx, selectorFor(workerKind, cluster), w)
}

// StreamRunnerLogs follows the log output of cluster's runner pod.
func (m *Manager) StreamRunnerLogs(ctx context.Context, cluster Cluster, w io.Writer) error {
	return m.streamPodLogs(ctx, selectorFor(runnerKind, cluster), w)
}

// streamPodLogs waits (up to 30s) for a pod matching selector to exist and
// have started, then follows its logs.
func (m *Manager) streamPodLogs(ctx context.Context, selector string, w io.Writer) error {
	pod, err := m.waitForPod(ctx, selector, 30*time.Second)
	if err != nil {
		return err
	}

	req := m.clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Follow: true,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("opening log stream for pod %s: %w", pod.Name, err)
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintln(w, scanner.Text()); err != nil {
			return err
		}
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
	return scanner.Err()
}

func (m *Manager) waitForPod(ctx context.Context, selector string, timeout time.Duration) (*corev1.Pod, error) {
	deadline := time.Now().Add(timeout)
	for {
		list, err := m.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("listing pods for %q: %w", selector, err)
		}

		if pod := mostRecentStartedPod(list.Items); pod != nil {
			return pod, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for a running pod matching %q", selector)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// mostRecentStartedPod returns the newest pod that has at least begun
// starting its container (logs aren't available for a pod stuck in
// Pending), or nil if none qualify yet.
func mostRecentStartedPod(pods []corev1.Pod) *corev1.Pod {
	var best *corev1.Pod
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase == corev1.PodPending {
			continue
		}
		if best == nil || p.CreationTimestamp.After(best.CreationTimestamp.Time) {
			best = p
		}
	}
	return best
}
