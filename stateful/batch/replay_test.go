package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-476
import (
	"testing"

	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestDispatcherReplaysRetainedCooldownRestartHistory(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(DispatcherWorkflow, workflow.RegisterOptions{Name: DispatcherWorkflowName})
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/cooldown-restart-v1.json"); err != nil {
		t.Fatal("dispatcher is incompatible with the retained V1 history", err)
	}
}
