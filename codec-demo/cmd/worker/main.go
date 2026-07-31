package main

import (
	"log"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"

	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/codec"
	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/workflows"
)

// TaskQueue matches the queue name learners target with `temporal workflow
// start --task-queue codec-demo` (see README.md).
const TaskQueue = "codec-demo"

func main() {
	aesCodec, err := codec.NewAESCodecFromEnv()
	if err != nil {
		log.Fatalf("initializing codec: %v", err)
	}

	// start.sh starts codec-demo before some Temporal health/namespace steps
	// complete, so the frontend may not be dialable yet -- retry instead of
	// fataling on the first failure so this pod doesn't crash-loop and abort
	// the whole script via `kubectl rollout status` (see start.sh's
	// ensure_postgres/verify_postgres comments for the same class of issue).
	var c client.Client
	for {
		c, err = client.Dial(client.Options{
			// Matches worker-control-ui's frontend addressing convention: the
			// cluster-1 Helm release's frontend Service, same namespace.
			HostPort:  "cluster-1-temporal-frontend:7233",
			Namespace: "default",
			DataConverter: converter.NewCodecDataConverter(
				converter.GetDefaultDataConverter(),
				aesCodec,
			),
		})
		if err == nil {
			break
		}
		log.Printf("waiting for Temporal frontend: %v", err)
		time.Sleep(5 * time.Second)
	}
	defer c.Close()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(workflows.CustomerOnboardingWorkflow)
	w.RegisterWorkflow(workflows.ProcessPaymentWorkflow)
	w.RegisterActivity(workflows.ChargeCardActivity)

	log.Printf("starting worker on task queue %q", TaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker run failed: %v", err)
	}
}
