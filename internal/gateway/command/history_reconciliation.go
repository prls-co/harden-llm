package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/traces"
)

const defaultHistoryReconciliationLimit = 10_000

type HistoryReconciliationConfig struct {
	Store      *postgres.Store
	Artifacts  HistoryArtifactCoordinator
	OwnerID    string
	AllOwners  bool
	Apply      bool
	PlanDigest string
	Limit      int
}

type HistoryArtifactCoordinator interface {
	VerifyArtifact(context.Context, postgres.ArtifactRecord) (bool, error)
	DeleteReconciledTrace(context.Context, string, string, string, string) error
}

type HistoryReconciliationReport struct {
	SchemaVersion        int    `json:"schemaVersion"`
	Mode                 string `json:"mode"`
	Scope                string `json:"scope"`
	PlanDigest           string `json:"planDigest"`
	CandidateTraces      int    `json:"candidateTraces"`
	ClassifiedTraces     int    `json:"classifiedTraces"`
	UnclassifiedTraces   int    `json:"unclassifiedTraces"`
	ObservationRows      int    `json:"observationRows"`
	ArtifactRows         int    `json:"artifactRows"`
	IntegrityArtifacts   int    `json:"integrityArtifacts"`
	UnavailableArtifacts int    `json:"unavailableArtifacts"`
	AppliedTraces        int    `json:"appliedTraces"`
	Truncated            bool   `json:"truncated"`
}

type historyReconciliationPlan struct {
	report     HistoryReconciliationReport
	candidates []classifiedRunlessTrace
}

type classifiedRunlessTrace struct {
	candidate postgres.RunlessTraceCandidate
	runID     string
	class     string
}

func ReconcileHistory(ctx context.Context, config HistoryReconciliationConfig) (HistoryReconciliationReport, error) {
	if config.Store == nil || config.Artifacts == nil {
		return HistoryReconciliationReport{}, errors.New("gateway command: history reconciliation stores are required")
	}
	if (strings.TrimSpace(config.OwnerID) == "") == !config.AllOwners {
		return HistoryReconciliationReport{}, errors.New("gateway command: select exactly one owner or all owners")
	}
	if config.Limit == 0 {
		config.Limit = defaultHistoryReconciliationLimit
	}
	plan, err := buildHistoryReconciliationPlan(ctx, config)
	if err != nil {
		return HistoryReconciliationReport{}, err
	}
	if !config.Apply {
		return plan.report, nil
	}
	plan.report.Mode = "apply"
	if len(plan.candidates) == 0 {
		return plan.report, nil
	}
	if plan.report.Truncated || plan.report.UnclassifiedTraces != 0 {
		return plan.report, errors.New("gateway command: reconciliation plan contains unclassified or truncated rows")
	}
	if len(config.PlanDigest) != 64 || !strings.EqualFold(config.PlanDigest, plan.report.PlanDigest) {
		return plan.report, errors.New("gateway command: reconciliation plan digest changed")
	}
	for _, item := range plan.candidates {
		if item.class != "legacy_deleted_execution" {
			return plan.report, errors.New("gateway command: reconciliation plan contains an unsupported classification")
		}
		if err := config.Artifacts.DeleteReconciledTrace(ctx, item.candidate.Trace.OwnerID, item.runID,
			item.candidate.Trace.TraceID, item.candidate.Fingerprint); err != nil {
			return plan.report, fmt.Errorf("gateway command: apply history reconciliation: %w", err)
		}
		plan.report.AppliedTraces++
	}
	return plan.report, nil
}

func buildHistoryReconciliationPlan(ctx context.Context, config HistoryReconciliationConfig) (historyReconciliationPlan, error) {
	ownerID := strings.TrimSpace(config.OwnerID)
	candidates, truncated, err := config.Store.RunlessTraceCandidates(ctx, ownerID, config.Limit)
	if err != nil {
		return historyReconciliationPlan{}, err
	}
	plan := historyReconciliationPlan{report: HistoryReconciliationReport{
		SchemaVersion: 1, Mode: "dry-run", Scope: "owner", CandidateTraces: len(candidates), Truncated: truncated,
	}}
	if config.AllOwners {
		plan.report.Scope = "all-owners"
	}
	for _, candidate := range candidates {
		item := classifiedRunlessTrace{candidate: candidate, class: "unclassified"}
		plan.report.ObservationRows += len(candidate.Observations)
		plan.report.ArtifactRows += len(candidate.Artifacts)
		if runID, ok := classifyLegacyDeletedExecution(candidate); ok {
			exists, existsErr := config.Store.RunIdentityExists(ctx, candidate.Trace.OwnerID, runID)
			if existsErr != nil {
				return historyReconciliationPlan{}, existsErr
			}
			if !exists {
				item.runID = runID
				item.class = "legacy_deleted_execution"
				for _, artifact := range candidate.Artifacts {
					available, inspectErr := config.Artifacts.VerifyArtifact(ctx, artifact)
					if inspectErr != nil {
						return historyReconciliationPlan{}, errors.New("gateway command: artifact integrity inspection failed")
					}
					if available {
						plan.report.IntegrityArtifacts++
					} else {
						plan.report.UnavailableArtifacts++
						item.class = "unclassified"
					}
				}
			}
		}
		if item.class == "legacy_deleted_execution" {
			plan.report.ClassifiedTraces++
		} else {
			plan.report.UnclassifiedTraces++
		}
		plan.candidates = append(plan.candidates, item)
	}
	plan.report.PlanDigest = historyPlanDigest(plan.report.Scope, ownerID, plan.candidates, truncated)
	return plan, nil
}

func classifyLegacyDeletedExecution(candidate postgres.RunlessTraceCandidate) (string, bool) {
	var record map[string]any
	if json.Unmarshal(candidate.Trace.Record, &record) != nil || record["schemaVersion"] != float64(1) {
		return "", false
	}
	runID, runOK := record["runId"].(string)
	traceID, traceOK := record["traceId"].(string)
	callID, callOK := record["callId"].(string)
	status, statusOK := record["status"].(string)
	_, attemptsOK := record["attempts"].([]any)
	_, cacheOK := record["cache"].(map[string]any)
	_, usageOK := record["usage"].(map[string]any)
	_, costOK := record["cost"].(map[string]any)
	if !runOK || !traceOK || !callOK || !statusOK || !attemptsOK || !cacheOK || !usageOK || !costOK ||
		!safeIdentifier(runID) || !safeIdentifier(callID) || traceID != candidate.Trace.TraceID ||
		(status != "succeeded" && status != "failed" && status != "timeout") {
		return "", false
	}
	for index, observation := range candidate.Observations {
		if observation.OwnerID != candidate.Trace.OwnerID || observation.TraceID != candidate.Trace.TraceID || observation.Sequence != index {
			return "", false
		}
	}
	if len(candidate.Artifacts) == 0 {
		return "", false
	}
	traceArtifact := false
	prefix := path.Join("llm-traces", traces.SafeObjectKeyComponent(candidate.Trace.OwnerID),
		traces.SafeObjectKeyComponent(runID), traces.SafeObjectKeyComponent(candidate.Trace.TraceID)) + "/"
	for _, artifact := range candidate.Artifacts {
		if artifact.OwnerID != candidate.Trace.OwnerID || artifact.TraceID != candidate.Trace.TraceID ||
			artifact.State != "available" || !strings.HasPrefix(artifact.ObjectKey, prefix) {
			return "", false
		}
		if artifact.Kind == "trace" {
			traceArtifact = true
		}
	}
	return runID, traceArtifact
}

func safeIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || (character == '.' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func historyPlanDigest(scope, ownerID string, candidates []classifiedRunlessTrace, truncated bool) string {
	ordered := append([]classifiedRunlessTrace(nil), candidates...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].candidate.Trace.OwnerID != ordered[right].candidate.Trace.OwnerID {
			return ordered[left].candidate.Trace.OwnerID < ordered[right].candidate.Trace.OwnerID
		}
		return ordered[left].candidate.Trace.TraceID < ordered[right].candidate.Trace.TraceID
	})
	digest := sha256.New()
	for _, value := range []string{"history-reconciliation-v1", scope, ownerID, fmt.Sprint(truncated)} {
		_, _ = fmt.Fprintf(digest, "%d:%s|", len(value), value)
	}
	for _, item := range ordered {
		for _, value := range []string{item.candidate.Trace.OwnerID, item.candidate.Trace.TraceID,
			item.candidate.Fingerprint, item.runID, item.class} {
			_, _ = fmt.Fprintf(digest, "%d:%s|", len(value), value)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}
