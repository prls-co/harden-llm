//go:build integration && temporalintegration

package integrationtest

import (
	"context"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/durationpb"
	"time"
)

// TemporalLease creates a namespace only in the runner-owned test service.
// Namespace deletion precedes disposal of its private client and service pool.
func TemporalLease(t testingTB) (client.Client, string) {
	t.Helper()
	if !poolEnabled() {
		t.Fatalf("TemporalLease requires the runner-owned integration service pool")
	}
	endpoint := requirePoolEndpoint(t, "HARDEN_LLM_TEST_TEMPORAL_ENDPOINT")
	namespace := "harden-test-" + randomLeaseToken(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := client.DialContext(ctx, client.Options{HostPort: endpoint, Namespace: namespace})
	if err != nil {
		t.Fatalf("connect Temporal test service: %v", err)
	}
	if _, err = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(time.Hour)}); err != nil {
		c.Close()
		t.Fatalf("register test namespace: %v", err)
	}
	t.Cleanup(func() {
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := c.OperatorService().DeleteNamespace(ctx, &operatorservice.DeleteNamespaceRequest{Namespace: namespace}); err != nil {
			t.Errorf("delete test Temporal namespace: %v", err)
		}
	})
	return c, namespace
}
