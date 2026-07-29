// Copied into a vendored omes checkout's scenarios/ directory at image build
// time (see worker-control-ui/runtime/Dockerfile) -- see that Dockerfile for
// why this lives outside the checkout instead of being a fork of omes.
package scenarios

import (
	"context"
	"fmt"

	"github.com/temporalio/omes/loadgen"
	"go.temporal.io/sdk/client"
)

// helloWorldExample mirrors internal/k8s.runnerExamples (Steady mode's
// lookup table) but as native Go values instead of a JSON string literal --
// the Temporal Go client serializes workflow args itself, so there's no JSON
// text to hand it here.
type helloWorldExample struct {
	workflowType string
	args         []any
}

var helloWorldExamples = map[int]helloWorldExample{
	// Hello world, no activity at all.
	1: {"ExecuteActivityWorkflow", []any{map[string]any{"Count": 0}}},
	// Hello world via a single activity call (the original default job).
	2: {"ExecuteActivityWorkflow", []any{map[string]any{
		"Count": 1, "Activity": "Echo", "Input": map[string]any{"Message": "Hello, World!"},
	}}},
	// Hello world via an activity, then a 5s timer -- two DSL steps, since a
	// single step's SleepSeconds runs *before* its Activity (see
	// internal/k8s.runnerExamples' comment on the same input).
	3: {"DSLWorkflow", []any{[]any{
		map[string]any{"a": "Echo", "i": map[string]any{"Message": "Hello, World!"}},
		map[string]any{"t": 5},
	}}},
}

func init() {
	loadgen.MustRegisterScenario(loadgen.Scenario{
		Description: "Dispatches one of the workshop's 3 hello-world example workflows (benchmark-workers) at a controlled rate against an already-running worker on the hello-world task queue. Options: 'example' (1, 2, or 3; default 2).",
		ExecutorFn: func() loadgen.Executor {
			return &loadgen.GenericExecutor{
				Execute: func(ctx context.Context, run *loadgen.Run) error {
					n := run.ScenarioOptionInt("example", 2)
					example, ok := helloWorldExamples[n]
					if !ok {
						return fmt.Errorf("unknown example %d (want 1, 2, or 3)", n)
					}
					_, err := run.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
						TaskQueue: "hello-world",
					}, example.workflowType, example.args...)
					// Fire-and-forget: we're measuring dispatch rate, not
					// completion rate, so don't wait for the workflow to finish.
					return err
				},
			}
		},
	})
}
