package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
)

const (
	artifactReconcileLimit         = 100
	artifactReconcileInterval      = 30 * time.Second
	artifactRetryDelay             = 30 * time.Second
	artifactIntegrityAuditInterval = 15 * time.Minute
)

type ArtifactObjectAccess interface {
	Put(context.Context, string, []byte, string) (hardenllm.ArtifactRef, error)
	Inspect(context.Context, string) (hardenllm.ArtifactRef, bool, error)
	PresignGet(context.Context, string, time.Duration) (string, error)
	DeleteMany(context.Context, []string) error
}

type ArtifactObjectScope func(ownerID string) (ArtifactObjectAccess, error)

type ArtifactCoordinatorConfig struct {
	Store     *postgres.Store
	Scope     ArtifactObjectScope
	Clock     func() time.Time
	NewID     func() (string, error)
	Logger    *slog.Logger
	Telemetry *Telemetry
}

type ArtifactCoordinator struct {
	store     *postgres.Store
	scope     ArtifactObjectScope
	clock     func() time.Time
	newID     func() (string, error)
	logger    *slog.Logger
	telemetry *Telemetry
}

type scopedArtifactCoordinator struct {
	coordinator *ArtifactCoordinator
	ownerID     string
}

type ArtifactReconcileSummary struct {
	Inspected   int
	Applied     int
	Completed   int
	Failed      int
	Audited     int
	Unavailable int
}

func NewArtifactCoordinator(config ArtifactCoordinatorConfig) (*ArtifactCoordinator, error) {
	if config.Store == nil || config.Scope == nil {
		return nil, errors.New("gateway: artifact journal and object scope are required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = newGatewayID
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.Telemetry == nil {
		config.Telemetry = newNoopTelemetry()
	}
	return &ArtifactCoordinator{
		store: config.Store, scope: config.Scope, clock: config.Clock,
		newID: config.NewID, logger: config.Logger, telemetry: config.Telemetry,
	}, nil
}

func (coordinator *ArtifactCoordinator) Scoped(ownerID string) (hardenllm.ArtifactStore, error) {
	if coordinator == nil || coordinator.store == nil || strings.TrimSpace(ownerID) == "" {
		return nil, errors.New("gateway: artifact coordinator scope is invalid")
	}
	if _, err := coordinator.scope(ownerID); err != nil {
		return nil, fmt.Errorf("gateway: artifact object scope is unavailable: %w", err)
	}
	return &scopedArtifactCoordinator{coordinator: coordinator, ownerID: ownerID}, nil
}

func (scope *scopedArtifactCoordinator) Put(context.Context, string, []byte, string) (hardenllm.ArtifactRef, error) {
	return hardenllm.ArtifactRef{}, errors.New("gateway: typed artifact publication is required")
}

func (scope *scopedArtifactCoordinator) PublishArtifact(ctx context.Context, publication hardenllm.ArtifactPublication) (hardenllm.ArtifactRef, error) {
	if scope == nil || scope.coordinator == nil || publication.OwnerID != scope.ownerID {
		return hardenllm.ArtifactRef{}, errors.New("gateway: artifact publication owner mismatch")
	}
	return scope.coordinator.PublishArtifact(ctx, publication)
}

func (scope *scopedArtifactCoordinator) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if scope == nil || scope.coordinator == nil {
		return "", errors.New("gateway: artifact coordinator is unavailable")
	}
	return scope.coordinator.PresignGet(ctx, scope.ownerID, key, ttl)
}

func (coordinator *ArtifactCoordinator) PublishArtifact(ctx context.Context, publication hardenllm.ArtifactPublication) (hardenllm.ArtifactRef, error) {
	now := coordinator.clock().UTC()
	digest := sha256.Sum256(publication.Content)
	record := postgres.ArtifactRecord{
		OwnerID: publication.OwnerID, RunID: publication.RunID, TraceID: publication.TraceID,
		ID: publication.ArtifactID, Kind: publication.Kind, ObjectKey: publication.ObjectKey,
		ContentType: publication.ContentType, SHA256: hex.EncodeToString(digest[:]),
		SizeBytes: int64(len(publication.Content)), State: "available", CreatedAt: now, UpdatedAt: now,
	}
	operation := postgres.ArtifactOperation{
		ID: postgres.ArtifactOperationID("publish", record), Action: "publish", State: "pending",
		OwnerID: record.OwnerID, RunID: record.RunID, TraceID: record.TraceID,
		ArtifactID: record.ID, Kind: record.Kind, ObjectKey: record.ObjectKey,
		ContentType: record.ContentType, SHA256: record.SHA256, SizeBytes: record.SizeBytes,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	stored, err := coordinator.store.BeginArtifactPublication(ctx, operation)
	if err != nil {
		return hardenllm.ArtifactRef{}, err
	}
	objectStore, err := coordinator.scope(publication.OwnerID)
	if err != nil {
		return hardenllm.ArtifactRef{}, fmt.Errorf("gateway: artifact object scope is unavailable: %w", err)
	}
	if stored.State == "completed" {
		reference, found, inspectErr := objectStore.Inspect(ctx, publication.ObjectKey)
		if inspectErr != nil || !found || !artifactReferenceMatches(reference, record) {
			return hardenllm.ArtifactRef{}, errors.New("gateway: completed artifact publication is inconsistent")
		}
		reference.ArtifactID, reference.Kind = record.ID, record.Kind
		return reference, nil
	}
	reference, err := objectStore.Put(ctx, publication.ObjectKey, publication.Content, publication.ContentType)
	if err != nil {
		coordinator.recordFailure(ctx, operation.ID, err)
		return hardenllm.ArtifactRef{}, err
	}
	if !artifactReferenceMatches(reference, record) {
		mismatch := errors.New("gateway: artifact object metadata mismatch")
		coordinator.recordFailure(ctx, operation.ID, mismatch)
		return hardenllm.ArtifactRef{}, mismatch
	}
	if err := coordinator.store.MarkArtifactOperationApplied(ctx, operation.ID, coordinator.clock().UTC()); err != nil {
		return hardenllm.ArtifactRef{}, err
	}
	reference.ArtifactID, reference.Kind = record.ID, record.Kind
	return reference, nil
}

func (coordinator *ArtifactCoordinator) PresignGet(ctx context.Context, ownerID, key string, ttl time.Duration) (string, error) {
	objectStore, err := coordinator.scope(ownerID)
	if err != nil {
		return "", err
	}
	return objectStore.PresignGet(ctx, key, ttl)
}

func (coordinator *ArtifactCoordinator) DeleteExecution(ctx context.Context, ownerID, runID, traceID string) error {
	batchID, err := coordinator.newID()
	if err != nil {
		return errors.New("gateway: generate artifact deletion batch ID")
	}
	batch, err := coordinator.store.BeginExecutionArtifactDeletion(ctx, batchID, ownerID, runID, traceID, coordinator.clock().UTC())
	if err != nil {
		return err
	}
	_, err = coordinator.applyDeletionBatch(ctx, batch)
	return err
}

func (coordinator *ArtifactCoordinator) ClearOwner(ctx context.Context, ownerID string) (int64, error) {
	batchID, err := coordinator.newID()
	if err != nil {
		return 0, errors.New("gateway: generate artifact deletion batch ID")
	}
	batch, err := coordinator.store.BeginOwnerArtifactDeletion(ctx, batchID, ownerID, coordinator.clock().UTC())
	if err != nil {
		return 0, err
	}
	return coordinator.applyDeletionBatch(ctx, batch)
}

func (coordinator *ArtifactCoordinator) VerifyArtifact(ctx context.Context, record postgres.ArtifactRecord) (bool, error) {
	if coordinator == nil || record.OwnerID == "" || record.ObjectKey == "" {
		return false, errors.New("gateway: artifact verification identity is invalid")
	}
	objectStore, err := coordinator.scope(record.OwnerID)
	if err != nil {
		return false, fmt.Errorf("gateway: artifact object scope is unavailable: %w", err)
	}
	reference, found, err := objectStore.Inspect(ctx, record.ObjectKey)
	if err != nil || !found {
		return false, err
	}
	return artifactReferenceMatches(reference, record), nil
}

func (coordinator *ArtifactCoordinator) DeleteReconciledTrace(ctx context.Context, ownerID, runID, traceID, fingerprint string) error {
	batchID, err := coordinator.newID()
	if err != nil {
		return errors.New("gateway: generate artifact reconciliation batch ID")
	}
	batch, err := coordinator.store.BeginReconciledTraceDeletion(
		ctx, batchID, ownerID, runID, traceID, fingerprint, coordinator.clock().UTC())
	if err != nil {
		return err
	}
	_, err = coordinator.applyDeletionBatch(ctx, batch)
	return err
}

func (coordinator *ArtifactCoordinator) applyDeletionBatch(ctx context.Context, batch postgres.ArtifactDeleteBatch) (int64, error) {
	objectStore, err := coordinator.scope(batch.OwnerID)
	if err != nil {
		return 0, fmt.Errorf("gateway: artifact object scope is unavailable: %w", err)
	}
	for _, operation := range batch.Operations {
		if operation.State == "object_applied" || operation.State == "completed" {
			continue
		}
		if err := objectStore.DeleteMany(ctx, []string{operation.ObjectKey}); err != nil {
			coordinator.recordFailure(ctx, operation.ID, err)
			return 0, fmt.Errorf("gateway: artifact deletion failed: %w", err)
		}
		if err := coordinator.store.MarkArtifactOperationApplied(ctx, operation.ID, coordinator.clock().UTC()); err != nil {
			return 0, err
		}
	}
	return coordinator.store.FinalizeArtifactDeleteBatch(ctx, batch.ID, coordinator.clock().UTC())
}

func (coordinator *ArtifactCoordinator) Reconcile(ctx context.Context) (ArtifactReconcileSummary, error) {
	var summary ArtifactReconcileSummary
	run, err := coordinator.store.WithArtifactReconcileLock(ctx, func(lockContext context.Context) error {
		operations, err := coordinator.store.PendingArtifactOperations(lockContext, coordinator.clock().UTC(), artifactReconcileLimit)
		if err != nil {
			return err
		}
		batches := make(map[string]struct{})
		for _, operation := range operations {
			summary.Inspected++
			if operation.Action == "publish" {
				if err := coordinator.reconcilePublication(lockContext, operation, &summary); err != nil {
					summary.Failed++
					continue
				}
				continue
			}
			if operation.BatchID != "" {
				batches[operation.BatchID] = struct{}{}
			}
			if operation.State == "pending" {
				objectStore, scopeErr := coordinator.scope(operation.OwnerID)
				if scopeErr != nil {
					coordinator.recordFailure(lockContext, operation.ID, scopeErr)
					summary.Failed++
					continue
				}
				if deleteErr := objectStore.DeleteMany(lockContext, []string{operation.ObjectKey}); deleteErr != nil {
					coordinator.recordFailure(lockContext, operation.ID, deleteErr)
					summary.Failed++
					continue
				}
				if err := coordinator.store.MarkArtifactOperationApplied(lockContext, operation.ID, coordinator.clock().UTC()); err != nil {
					summary.Failed++
					continue
				}
				summary.Applied++
			}
		}
		for batchID := range batches {
			if _, err := coordinator.store.FinalizeArtifactDeleteBatch(lockContext, batchID, coordinator.clock().UTC()); err == nil {
				summary.Completed++
			}
		}
		if err := coordinator.auditAvailableArtifacts(lockContext, &summary); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		coordinator.telemetry.RecordArtifactReconciliation(ctx, 0, 0, "error")
		return summary, err
	}
	if !run {
		return summary, nil
	}
	backlog, err := coordinator.store.ArtifactOperationBacklog(ctx, coordinator.clock().UTC())
	if err != nil {
		coordinator.telemetry.RecordArtifactReconciliation(ctx, 0, 0, "error")
		return summary, err
	}
	outcome := "success"
	if summary.Failed > 0 {
		outcome = "partial"
	}
	coordinator.telemetry.RecordArtifactReconciliation(ctx, backlog.Pending, backlog.OldestAge, outcome)
	coordinator.logger.InfoContext(ctx, "artifact reconciliation completed",
		"inspected", summary.Inspected, "applied", summary.Applied,
		"completed", summary.Completed, "failed", summary.Failed,
		"audited", summary.Audited, "unavailable", summary.Unavailable,
		"pending", backlog.Pending, "oldest_pending_seconds", backlog.OldestAge.Seconds())
	return summary, nil
}

func (coordinator *ArtifactCoordinator) auditAvailableArtifacts(ctx context.Context, summary *ArtifactReconcileSummary) error {
	records, err := coordinator.store.AvailableArtifactsForAudit(
		ctx, coordinator.clock().UTC().Add(-artifactIntegrityAuditInterval), artifactReconcileLimit)
	if err != nil {
		return err
	}
	for _, record := range records {
		summary.Audited++
		objectStore, scopeErr := coordinator.scope(record.OwnerID)
		if scopeErr != nil {
			summary.Failed++
			continue
		}
		reference, found, inspectErr := objectStore.Inspect(ctx, record.ObjectKey)
		if inspectErr != nil {
			summary.Failed++
			continue
		}
		verified := found && artifactReferenceMatches(reference, record)
		changed, markErr := coordinator.store.RecordArtifactIntegrity(ctx, record, verified, coordinator.clock().UTC())
		if markErr != nil {
			return markErr
		}
		if changed && !verified {
			summary.Unavailable++
		}
	}
	return nil
}

func (coordinator *ArtifactCoordinator) reconcilePublication(ctx context.Context, operation postgres.ArtifactOperation, summary *ArtifactReconcileSummary) error {
	objectStore, err := coordinator.scope(operation.OwnerID)
	if err != nil {
		coordinator.recordFailure(ctx, operation.ID, err)
		return err
	}
	reference, found, err := objectStore.Inspect(ctx, operation.ObjectKey)
	if err != nil {
		coordinator.recordFailure(ctx, operation.ID, err)
		return err
	}
	record := artifactRecordFromOperation(operation)
	if operation.State == "pending" {
		if !found {
			if err := coordinator.store.CompleteArtifactOperation(ctx, operation.ID, true, coordinator.clock().UTC()); err != nil {
				return err
			}
			summary.Completed++
			return nil
		}
		if !artifactReferenceMatches(reference, record) {
			if err := objectStore.DeleteMany(ctx, []string{operation.ObjectKey}); err != nil {
				coordinator.recordFailure(ctx, operation.ID, err)
				return err
			}
			if err := coordinator.store.CompleteArtifactOperation(ctx, operation.ID, true, coordinator.clock().UTC()); err != nil {
				return err
			}
			summary.Completed++
			return nil
		}
		if err := coordinator.store.MarkArtifactOperationApplied(ctx, operation.ID, coordinator.clock().UTC()); err != nil {
			return err
		}
		summary.Applied++
		operation.State = "object_applied"
	}
	matches, err := coordinator.store.ArtifactMetadataMatches(ctx, operation)
	if err != nil {
		return err
	}
	if !matches {
		if err := objectStore.DeleteMany(ctx, []string{operation.ObjectKey}); err != nil {
			coordinator.recordFailure(ctx, operation.ID, err)
			return err
		}
	}
	if err := coordinator.store.CompleteArtifactOperation(ctx, operation.ID, !matches, coordinator.clock().UTC()); err != nil {
		return err
	}
	summary.Completed++
	return nil
}

func (coordinator *ArtifactCoordinator) RunReconciler(ctx context.Context) {
	if coordinator == nil {
		return
	}
	_, _ = coordinator.Reconcile(ctx)
	ticker := time.NewTicker(artifactReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = coordinator.Reconcile(ctx)
		}
	}
}

func (coordinator *ArtifactCoordinator) recordFailure(ctx context.Context, operationID string, err error) {
	now := coordinator.clock().UTC()
	_ = coordinator.store.RecordArtifactOperationFailure(ctx, operationID, artifactErrorCategory(err), now.Add(artifactRetryDelay), now)
}

func artifactErrorCategory(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "unavailable"
}

func artifactReferenceMatches(reference hardenllm.ArtifactRef, record postgres.ArtifactRecord) bool {
	return reference.Key == record.ObjectKey && strings.EqualFold(reference.SHA256, record.SHA256) &&
		reference.SizeBytes == record.SizeBytes && reference.ContentType == record.ContentType
}

func artifactRecordFromOperation(operation postgres.ArtifactOperation) postgres.ArtifactRecord {
	return postgres.ArtifactRecord{
		OwnerID: operation.OwnerID, RunID: operation.RunID, TraceID: operation.TraceID,
		ID: operation.ArtifactID, Kind: operation.Kind, ObjectKey: operation.ObjectKey,
		ContentType: operation.ContentType, SHA256: operation.SHA256,
		SizeBytes: operation.SizeBytes, State: "available",
		CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
	}
}
