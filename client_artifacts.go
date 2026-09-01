package hardenllm

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/redaction"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/internal/traces"
)

const artifactPersistenceTimeout = 2 * time.Second

func (client *Client) persistCallArtifacts(
	ctx context.Context,
	record coreruntime.CallRecord,
	callContext coreruntime.ObservabilityContext,
	startedAt time.Time,
	completedAt time.Time,
	terminalErr error,
	secrets []string,
) []ArtifactRef {
	if client == nil || client.options.Artifacts == nil || record.CallID == "" || record.TraceID == "" {
		return nil
	}
	trace := traces.Project(record, callContext, startedAt, completedAt, terminalErr)
	projections, err := traces.ArtifactProjections(trace, record.ParseFailureResponse, secrets...)
	if err != nil {
		client.logArtifactFailure(ctx, "projection", err, secrets)
		return nil
	}
	persistContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), artifactPersistenceTimeout)
	defer cancel()

	secondary := make([]ArtifactRef, 0, len(projections)-1)
	for _, projection := range projections {
		if projection.Kind == traces.ArtifactKindTrace {
			continue
		}
		reference, persistErr := client.putArtifact(persistContext, projection, record, callContext)
		if persistErr != nil {
			safeMessage := client.logArtifactFailure(ctx, projection.Kind, persistErr, secrets)
			traces.AddObservation(&trace, "artifact.persistence", "failure", map[string]any{
				"kind": projection.Kind, "message": safeMessage,
			})
			continue
		}
		secondary = append(secondary, reference)
	}

	updated, err := traces.ArtifactProjections(trace, record.ParseFailureResponse, secrets...)
	if err != nil {
		client.logArtifactFailure(ctx, "trace-projection", err, secrets)
		return secondary
	}
	for _, projection := range updated {
		if projection.Kind != traces.ArtifactKindTrace {
			continue
		}
		reference, persistErr := client.putArtifact(persistContext, projection, record, callContext)
		if persistErr != nil {
			client.logArtifactFailure(ctx, projection.Kind, persistErr, secrets)
			return secondary
		}
		return append([]ArtifactRef{reference}, secondary...)
	}
	return secondary
}

func (client *Client) putArtifact(
	ctx context.Context,
	projection traces.ArtifactProjection,
	record coreruntime.CallRecord,
	callContext coreruntime.ObservabilityContext,
) (reference ArtifactRef, err error) {
	ctx, endArtifact := client.telemetry.StartArtifact(ctx, projection.Kind)
	defer func() { endArtifact(err) }()
	if publisher, ok := client.options.Artifacts.(ArtifactPublisher); ok {
		reference, err = publisher.PublishArtifact(ctx, ArtifactPublication{
			OwnerID: callContext.OrganizationID, RunID: callContext.RunID, TraceID: record.TraceID,
			ArtifactID: projection.ArtifactID, Kind: projection.Kind, ObjectKey: projection.Key,
			Content: append([]byte(nil), projection.Content...), ContentType: projection.ContentType,
		})
	} else {
		reference, err = client.options.Artifacts.Put(ctx, projection.Key, projection.Content, projection.ContentType)
	}
	if err != nil {
		return ArtifactRef{}, err
	}
	if reference.Key != projection.Key || reference.ContentType != projection.ContentType || reference.SizeBytes != int64(len(projection.Content)) {
		return ArtifactRef{}, errors.New("artifact store returned metadata that does not match the persisted content")
	}
	digest := strings.TrimPrefix(reference.SHA256, "sha256:")
	decoded, decodeErr := hex.DecodeString(digest)
	if decodeErr != nil || len(decoded) != 32 {
		return ArtifactRef{}, errors.New("artifact store returned an invalid SHA-256 digest")
	}
	reference.ArtifactID = projection.ArtifactID
	reference.Kind = projection.Kind
	return reference, nil
}

func (client *Client) logArtifactFailure(ctx context.Context, kind string, err error, secrets []string) string {
	redactor := redaction.New(secrets...)
	message := redactor.Text(err.Error())
	if len(message) > 240 {
		message = message[:240] + "..."
	}
	client.options.Logger.WarnContext(ctx, "artifact persistence failed", "kind", kind, "error", message)
	return message
}
