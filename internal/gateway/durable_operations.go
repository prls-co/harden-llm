package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	DurableOperationSchemaVersion = "harden-llm-operation.v1"
	DurableOperationWorkflowName  = "HardenDurableOperationWorkflow"
	DurableOperationActivity      = "HardenDurableOperationActivity"
	DurableOperationTaskQueue     = "harden-llm-durable-operations.v1"
	DurableOperationServiceName   = "harden-llm"
)

type DurableOperationInput struct {
	AccountID   string
	ServiceName string
	OperationID string
	InputDigest string
	Run         RunInput
}

type DurableOperationResult struct {
	OperationID  string    `json:"operationId"`
	Status       string    `json:"status"`
	Output       RunOutput `json:"output"`
	State        RunState  `json:"state"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
}

type DurableOperationView struct {
	SchemaVersion string          `json:"schemaVersion"`
	OperationID   string          `json:"operationId"`
	AccountID     string          `json:"accountId"`
	InputDigest   string          `json:"inputDigest"`
	Status        string          `json:"status"`
	Output        json.RawMessage `json:"output,omitempty"`
	Error         *DurableError   `json:"error,omitempty"`
	RunID         string          `json:"runId,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

type DurableError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DurableOperationService struct {
	store    *postgres.Store
	runs     *RunService
	temporal WorkflowStarter
	clock    func() time.Time
}

type WorkflowStarter interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	CancelWorkflow(context.Context, string, string) error
}

func NewDurableOperationService(store *postgres.Store, runs *RunService, temporalClient WorkflowStarter) (*DurableOperationService, error) {
	if store == nil || runs == nil || temporalClient == nil {
		return nil, errors.New("gateway: durable operation store, run service, and Temporal client are required")
	}
	return &DurableOperationService{store: store, runs: runs, temporal: temporalClient, clock: time.Now}, nil
}

func (service *DurableOperationService) Submit(ctx context.Context, input DurableOperationInput) (DurableOperationView, error) {
	if err := validateDurableInput(input); err != nil {
		return DurableOperationView{}, err
	}
	requestDocument, err := json.Marshal(input.Run)
	if err != nil {
		return DurableOperationView{}, errors.New("gateway: encode durable operation input")
	}
	now := service.clock().UTC()
	record, created, err := service.store.AdmitDurableOperation(ctx, postgres.DurableOperationRecord{
		AccountID: input.AccountID, ServiceName: input.ServiceName, OperationID: input.OperationID,
		InputDigest: input.InputDigest, Status: "accepted", Request: requestDocument, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return DurableOperationView{}, err
	}
	if created {
		workflowID := DurableWorkflowID(input.AccountID, input.ServiceName, input.OperationID)
		_, err = service.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: workflowID, TaskQueue: DurableOperationTaskQueue,
			WorkflowExecutionTimeout: 24 * time.Hour,
		}, DurableOperationWorkflow, input)
		if err != nil {
			updated := record
			updated.Status = "unresolved"
			updated.ErrorCode = "workflow_admission_failed"
			updated.ErrorMessage = "Harden could not start the durable operation workflow."
			updated.UpdatedAt = service.clock().UTC()
			_ = service.store.UpdateDurableOperation(context.WithoutCancel(ctx), updated)
			return DurableOperationView{}, fmt.Errorf("gateway: start durable operation workflow: %w", err)
		}
	}
	return viewDurableOperation(record), nil
}

func (service *DurableOperationService) Get(ctx context.Context, accountID, serviceName, operationID string) (DurableOperationView, error) {
	record, err := service.store.DurableOperation(ctx, accountID, serviceName, operationID)
	if err != nil {
		return DurableOperationView{}, err
	}
	return viewDurableOperation(record), nil
}

func (service *DurableOperationService) Cancel(ctx context.Context, accountID, serviceName, operationID string) (DurableOperationView, error) {
	record, err := service.store.CancelDurableOperation(ctx, accountID, serviceName, operationID, service.clock().UTC())
	if err != nil {
		return DurableOperationView{}, err
	}
	if record.Status == "cancelled" {
		workflowID := DurableWorkflowID(accountID, serviceName, operationID)
		if err := service.temporal.CancelWorkflow(ctx, workflowID, ""); err != nil {
			// The database cancellation is authoritative. A completed workflow may
			// legitimately have no cancellable execution left.
			var workflowNotFound *serviceerror.NotFound
			if !errors.As(err, &workflowNotFound) {
				return DurableOperationView{}, fmt.Errorf("gateway: cancel durable operation workflow: %w", err)
			}
		}
	}
	return viewDurableOperation(record), nil
}

func DurableOperationWorkflow(ctx workflow.Context, input DurableOperationInput) (DurableOperationResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    2,
		},
	}
	activityContext := workflow.WithActivityOptions(ctx, options)
	var result DurableOperationResult
	if err := workflow.ExecuteActivity(activityContext, DurableOperationActivity, input).Get(ctx, &result); err != nil {
		return DurableOperationResult{OperationID: input.OperationID, Status: "unresolved", ErrorCode: "durable_activity_failed", ErrorMessage: "Harden durable activity did not publish a terminal result."}, err
	}
	return result, nil
}

type DurableOperationActivities struct {
	Store *postgres.Store
	Runs  *RunService
}

func (activities *DurableOperationActivities) Execute(ctx context.Context, input DurableOperationInput) (DurableOperationResult, error) {
	if activities == nil || activities.Store == nil || activities.Runs == nil {
		return DurableOperationResult{}, errors.New("gateway: durable activity dependencies are required")
	}
	record, err := activities.Store.DurableOperation(ctx, input.AccountID, input.ServiceName, input.OperationID)
	if err != nil {
		return DurableOperationResult{}, err
	}
	if record.InputDigest != input.InputDigest {
		return DurableOperationResult{}, postgres.ErrDurableOperationConflict
	}
	if record.Status == "cancelled" {
		return DurableOperationResult{OperationID: input.OperationID, Status: "cancelled"}, nil
	}
	if record.Status == "succeeded" || record.Status == "failed" || record.Status == "unresolved" {
		return decodeDurableResult(record)
	}
	if record.Status == "running" {
		// A retry after the worker lost the activity acknowledgement cannot
		// prove whether the provider received the request. Do not blindly issue
		// a second provider call; expose the honest unresolved state.
		record.Status = "unresolved"
		record.ErrorCode = "provider_outcome_unknown"
		record.ErrorMessage = "The Harden worker stopped after execution began and the provider outcome is unknown."
		record.UpdatedAt = time.Now().UTC()
		if err := activities.Store.UpdateDurableOperationFromStatus(context.WithoutCancel(ctx), record, "running"); err != nil {
			if errors.Is(err, postgres.ErrDurableOperationStateConflict) {
				current, loadErr := activities.Store.DurableOperation(context.WithoutCancel(ctx), input.AccountID, input.ServiceName, input.OperationID)
				if loadErr != nil {
					return DurableOperationResult{}, loadErr
				}
				return decodeDurableResult(current)
			}
			return DurableOperationResult{}, err
		}
		return decodeDurableResult(record)
	}
	record.Status = "running"
	record.UpdatedAt = time.Now().UTC()
	if err := activities.Store.UpdateDurableOperationFromStatus(ctx, record, "accepted"); err != nil {
		if errors.Is(err, postgres.ErrDurableOperationStateConflict) {
			current, loadErr := activities.Store.DurableOperation(ctx, input.AccountID, input.ServiceName, input.OperationID)
			if loadErr != nil {
				return DurableOperationResult{}, loadErr
			}
			if current.Status == "cancelled" || current.Status == "succeeded" || current.Status == "failed" || current.Status == "unresolved" {
				return decodeDurableResult(current)
			}
		}
		return DurableOperationResult{}, err
	}
	runID := durableRunID(input.AccountID, input.ServiceName, input.OperationID)
	output, state, runErr := activities.Runs.RunWithID(ctx, input.AccountID, input.Run, runID)
	current, loadErr := activities.Store.DurableOperation(context.WithoutCancel(ctx), input.AccountID, input.ServiceName, input.OperationID)
	if loadErr != nil {
		return DurableOperationResult{}, loadErr
	}
	if current.Status == "cancelled" || current.Status == "succeeded" || current.Status == "failed" || current.Status == "unresolved" {
		return decodeDurableResult(current)
	}
	record = current
	record.RunID, record.TraceID = output.RunID, output.TraceID
	record.Result, _ = json.Marshal(DurableOperationResult{OperationID: input.OperationID, Status: output.Status, Output: output, State: state})
	record.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		record.Status = "failed"
		record.ErrorCode = "provider_run_failed"
		record.ErrorMessage = "The Harden provider operation failed."
	} else {
		record.Status = "succeeded"
	}
	if err := activities.Store.UpdateDurableOperationFromStatus(context.WithoutCancel(ctx), record, "running"); err != nil {
		if errors.Is(err, postgres.ErrDurableOperationStateConflict) {
			latest, loadErr := activities.Store.DurableOperation(context.WithoutCancel(ctx), input.AccountID, input.ServiceName, input.OperationID)
			if loadErr != nil {
				return DurableOperationResult{}, loadErr
			}
			return decodeDurableResult(latest)
		}
		return DurableOperationResult{}, err
	}
	result, decodeErr := decodeDurableResult(record)
	if decodeErr != nil {
		return DurableOperationResult{}, decodeErr
	}
	return result, nil
}

func validateDurableInput(input DurableOperationInput) error {
	if strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.ServiceName) == "" || strings.TrimSpace(input.OperationID) == "" || len(input.OperationID) > 128 || len(input.InputDigest) != 64 {
		return errors.New("gateway: durable operation identity is invalid")
	}
	if !validDurableCallerService(input.ServiceName) {
		return errors.New("gateway: durable operation service is invalid")
	}
	if _, err := hex.DecodeString(input.InputDigest); err != nil {
		return errors.New("gateway: durable operation input digest is invalid")
	}
	if err := validateRunInput(input.Run); err != nil {
		return err
	}
	return nil
}

func validDurableCallerService(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 64 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}

func viewDurableOperation(record postgres.DurableOperationRecord) DurableOperationView {
	view := DurableOperationView{
		SchemaVersion: DurableOperationSchemaVersion, OperationID: record.OperationID, AccountID: record.AccountID,
		InputDigest: record.InputDigest, Status: record.Status, RunID: record.RunID, TraceID: record.TraceID,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if len(record.Result) > 0 {
		var result DurableOperationResult
		if json.Unmarshal(record.Result, &result) == nil {
			encoded, _ := json.Marshal(result.Output)
			view.Output = encoded
		}
	}
	if record.ErrorCode != "" {
		view.Error = &DurableError{Code: record.ErrorCode, Message: record.ErrorMessage}
	}
	return view
}

func decodeDurableResult(record postgres.DurableOperationRecord) (DurableOperationResult, error) {
	if len(record.Result) == 0 {
		return DurableOperationResult{OperationID: record.OperationID, Status: record.Status, ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage}, nil
	}
	var result DurableOperationResult
	if err := json.Unmarshal(record.Result, &result); err != nil {
		return DurableOperationResult{}, fmt.Errorf("gateway: decode durable operation result: %w", err)
	}
	result.Status = record.Status
	result.ErrorCode, result.ErrorMessage = record.ErrorCode, record.ErrorMessage
	return result, nil
}

func DurableWorkflowID(accountID, serviceName, operationID string) string {
	digest := sha256.Sum256([]byte(accountID + "\x00" + serviceName + "\x00" + operationID))
	return "harden-op-" + hex.EncodeToString(digest[:])[:40]
}

func durableRunID(accountID, serviceName, operationID string) string {
	digest := sha256.Sum256([]byte("run\x00" + accountID + "\x00" + serviceName + "\x00" + operationID))
	return "harden-run-" + hex.EncodeToString(digest[:])[:40]
}
