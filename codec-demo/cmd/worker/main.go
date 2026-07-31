package main

import (
	"log"

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

	c, err := client.Dial(client.Options{
		// Matches worker-control-ui's frontend addressing convention: the
		// cluster-1 Helm release's frontend Service, same namespace.
		HostPort:  "cluster-1-temporal-frontend:7233",
		Namespace: "default",
		DataConverter: converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(),
			aesCodec,
		),
	})
	if err != nil {
		log.Fatalf("creating Temporal client: %v", err)
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
