package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-511

import (
	"context"
	"errors"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"testing"
)

type closedPoolControl struct {
	client.Client
	queried bool
}

func (c *closedPoolControl) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Type: &commonpb.WorkflowType{Name: PoolWorkflowName}, Status: enums.WORKFLOW_EXECUTION_STATUS_TERMINATED}}, nil
}
func (c *closedPoolControl) QueryWorkflow(context.Context, string, string, string, ...interface{}) (converter.EncodedValue, error) {
	c.queried = true
	return nil, ErrUnavailable
}
func TestPoolUpgradeNeverReportsTerminatedAuthorityReady(t *testing.T) {
	t.Parallel()
	control := &closedPoolControl{}
	if _, err := WaitPoolUpgrade(context.Background(), control, "pool", nil); !errors.Is(err, ErrGenerationFenced) || control.queried {
		t.Fatal("closed workflow snapshot was treated as live admission authority", err, control.queried)
	}
}
