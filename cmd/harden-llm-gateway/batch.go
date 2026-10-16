package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/stateful"
	"github.com/prls-co/harden-llm/stateful/batch"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type batchSettings struct {
	enabled                                         bool
	address, namespace, taskQueue, poolID, upstream string
	policy                                          batch.Policy
	jobTimeout                                      time.Duration
}

func loadBatchSettings(getenv func(string) string, connections []hardenllm.Connection) (s batchSettings, err error) {
	v := strings.TrimSpace(getenv("HARDEN_LLM_BATCH_ENABLED"))
	if v == "" || v == "false" {
		return s, nil
	}
	if v != "true" {
		return s, errors.New("configuration: HARDEN_LLM_BATCH_ENABLED must be true or false")
	}
	s.enabled = true
	s.address = strings.TrimSpace(getenv("HARDEN_LLM_TEMPORAL_ADDRESS"))
	s.namespace = strings.TrimSpace(getenv("HARDEN_LLM_TEMPORAL_NAMESPACE"))
	s.taskQueue = strings.TrimSpace(getenv("HARDEN_LLM_BATCH_TASK_QUEUE"))
	s.poolID = strings.TrimSpace(getenv("HARDEN_LLM_BATCH_POOL_ID"))
	s.upstream = strings.TrimSpace(getenv("HARDEN_LLM_BATCH_UPSTREAM"))
	if s.taskQueue == "" {
		s.taskQueue = "harden-llm-batch-v1"
	}
	host, port, e := net.SplitHostPort(s.address)
	number, _ := strconv.Atoi(port)
	if e != nil || host == "" || number < 1 || number > 65535 || strings.ContainsAny(host, "/@\\ \r\n\t") {
		return s, errors.New("configuration: HARDEN_LLM_TEMPORAL_ADDRESS must be a self-hosted host:port")
	}
	if !validConfigLabel(s.namespace, 128) || s.namespace == "" || !validConfigLabel(s.taskQueue, 128) || !validConfigLabel(s.poolID, 128) || s.poolID == "" {
		return s, errors.New("configuration: explicit Temporal namespace, batch task queue and CPA pool ID are required")
	}
	found := false
	for _, c := range connections {
		if c.ID == s.upstream && c.Provider == "cpa" && c.Protocol == "responses" {
			found = true
		}
	}
	if !found {
		return s, errors.New("configuration: HARDEN_LLM_BATCH_UPSTREAM must select one configured CPA Responses connection")
	}
	s.policy = batch.DefaultPolicy()
	for _, field := range []struct {
		name string
		dest *int
	}{{"CONCURRENCY", &s.policy.Concurrency}, {"BURST", &s.policy.Burst}, {"START_RATE", &s.policy.StartRate}, {"MAX_START_RATE", &s.policy.MaximumRate}} {
		if raw := strings.TrimSpace(getenv("HARDEN_LLM_BATCH_" + field.name)); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil {
				return s, fmt.Errorf("configuration: invalid HARDEN_LLM_BATCH_%s", field.name)
			}
			*field.dest = n
		}
	}
	if raw := strings.TrimSpace(getenv("HARDEN_LLM_BATCH_REFILL_ESTIMATE_MS")); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1000 || n > 3600000 {
			return s, errors.New("configuration: invalid HARDEN_LLM_BATCH_REFILL_ESTIMATE_MS")
		}
		s.policy.RefillEstimate = time.Duration(n) * time.Millisecond
	}
	if err = s.policy.Validate(); err != nil {
		return s, err
	}
	s.jobTimeout = 2 * time.Hour
	if raw := strings.TrimSpace(getenv("HARDEN_LLM_BATCH_JOB_TIMEOUT_SECONDS")); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 10 || n > 7200 {
			return s, errors.New("configuration: invalid HARDEN_LLM_BATCH_JOB_TIMEOUT_SECONDS")
		}
		s.jobTimeout = time.Duration(n) * time.Second
	}
	return s, nil
}
func configureBatch(ctx context.Context, c serverConfig, pool *pgxpool.Pool, artifacts *stateful.ArtifactStore, telemetry *gateway.TelemetryRuntime) (*batch.Service, *batch.Activities, client.Client, error) {
	if !c.batch.enabled {
		return nil, nil, nil, nil
	}
	logger := telemetry.Logger()
	if pool == nil || artifacts == nil {
		return nil, nil, nil, errors.New("batch: PostgreSQL and Garage must be configured")
	}
	var schemaVersion int
	if err := pool.QueryRow(ctx, "SELECT version FROM hllm_batch_schema_version WHERE singleton").Scan(&schemaVersion); err != nil || schemaVersion != 1 {
		return nil, nil, nil, errors.New("batch: run the explicit schema command before serving jobs")
	}
	temporalClient, err := client.DialContext(ctx, client.Options{HostPort: c.batch.address, Namespace: c.batch.namespace})
	if err != nil {
		return nil, nil, nil, errors.New("batch: cannot connect to self-hosted Temporal")
	}
	if _, err = temporalClient.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{Namespace: c.batch.namespace}); err != nil {
		temporalClient.Close()
		return nil, nil, nil, errors.New("batch: the configured Temporal namespace is unavailable")
	}
	var connection hardenllm.Connection
	for _, v := range c.connections {
		if v.ID == c.batch.upstream {
			connection = v
		}
	}
	core, err := stateful.NewClient(stateful.ClientOptions{Connections: []hardenllm.Connection{connection}, DefaultConnection: connection.ID, Logger: logger, TracerProvider: telemetry.TracerProvider(), MeterProvider: telemetry.MeterProvider(), EndpointPolicy: hardenllm.EndpointPolicy{AllowedHosts: c.allowedHosts, PrivateAllowedHosts: c.privateAllowedHosts, PrivateAllowlist: c.privateAllowlist, MaxConnsPerHost: c.batch.policy.Concurrency, ResponseHeaderTimeout: 180 * time.Second}})
	if err != nil {
		temporalClient.Close()
		return nil, nil, nil, err
	}
	store := &batch.PostgresStore{Pool: pool}
	svc := &batch.Service{Store: store, Starter: batch.TemporalStarter{Client: temporalClient, TaskQueue: c.batch.taskQueue}, Artifacts: artifacts, Upstream: c.batch.upstream, JobTimeout: c.batch.jobTimeout}
	activities := &batch.Activities{Store: store, Artifacts: artifacts, Temporal: temporalClient, TaskQueue: c.batch.taskQueue, PoolID: c.batch.poolID, Policy: c.batch.policy, Logger: logger, Encode: httpapi.EncodeBatchResponse, Execute: func(ctx context.Context, _ string, r hardenllm.Request) (hardenllm.Result, error) {
		return core.Call(ctx, r)
	}}
	return svc, activities, temporalClient, nil
}
func applyGatewaySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if err := stateful.ApplySchema(ctx, pool); err != nil {
		return err
	}
	return batch.ApplySchema(ctx, pool)
}
func runBatchPolicy(ctx context.Context, stdout io.Writer, getenv func(string) string) error {
	c, err := loadServerConfig(getenv)
	if err != nil {
		return err
	}
	if !c.batch.enabled {
		return errors.New("batch policy requires HARDEN_LLM_BATCH_ENABLED=true")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	temporalClient, err := client.DialContext(ctx, client.Options{HostPort: c.batch.address, Namespace: c.batch.namespace})
	if err != nil {
		return errors.New("batch: cannot connect to self-hosted Temporal")
	}
	defer temporalClient.Close()
	if err = temporalClient.SignalWorkflow(ctx, "hllm-cpa-pool-v1/"+c.batch.poolID, "", "policy", c.batch.policy); err != nil {
		return errors.New("batch: policy signal was not acknowledged; inspect the configured dispatcher")
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"status": "policy_signal_acknowledged", "pool_id": c.batch.poolID, "policy": c.batch.policy})
}
