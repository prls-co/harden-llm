package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

const (
	maximumProfileBodyBytes = 256 << 10
	maximumBundleBodyBytes  = 2 << 20
	maximumRunBodyBytes     = 256 << 10
	defaultHistoryPageSize  = 20
	maximumHistoryPageSize  = 100
)

func (api *API) getState(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	state, err := api.resources.State(request.Context(), mustPrincipal(request.Context()).OwnerID)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, nil, state)
}

func (api *API) saveState(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	var state gateway.ClientState
	if failure := decodeJSON(writer, request, maximumJSONBodyBytes, &state); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	state, err := api.resources.SaveState(request.Context(), mustPrincipal(request.Context()).OwnerID, state)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, nil, state)
}

func (api *API) listProfiles(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	states, err := api.resources.Profiles(request.Context(), mustPrincipal(request.Context()).OwnerID)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"profiles": states, "defaults": map[string]any{"recoveryPolicy": hardenllm.DefaultStructuredRecoveryPolicy()}}, map[string]any{})
}

func (api *API) saveProfile(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	var input struct {
		Profile      profiles.Profile            `json:"profile"`
		CredentialID string                      `json:"credentialId"`
		Credential   *profiles.CredentialPayload `json:"credential,omitempty"`
	}
	if failure := decodeJSON(writer, request, maximumProfileBodyBytes, &input); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	profileID := chi.URLParam(request, "profileID")
	if profileID == "" || input.Profile.LLMProfile != profileID {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "The profile path and document identity must match.")
		return
	}
	// Model discovery state is backend-owned and cannot be overwritten by a
	// profile-edit payload.
	input.Profile.Models = nil
	input.Profile.LastModelRefreshAt = nil
	state, err := api.resources.SaveProfile(request.Context(), gateway.SaveProfileRequest{
		OwnerID: mustPrincipal(request.Context()).OwnerID, ProfileID: profileID, Profile: input.Profile,
		CredentialID: input.CredentialID, Credential: input.Credential,
	})
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, state, map[string]any{})
}

func (api *API) deleteProfile(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	profileID := chi.URLParam(request, "profileID")
	if err := api.resources.DeleteProfile(request.Context(), mustPrincipal(request.Context()).OwnerID, profileID); err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"deleted": true, "profileId": profileID}, map[string]any{})
}

func (api *API) refreshProfileModels(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	state, err := api.resources.RefreshModels(request.Context(), mustPrincipal(request.Context()).OwnerID, chi.URLParam(request, "profileID"))
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, state, map[string]any{})
}

func (api *API) exportProfileBundle(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	bundle, err := api.resources.ExportBundle(request.Context(), mustPrincipal(request.Context()).OwnerID)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, bundle, map[string]any{})
}

func (api *API) importProfileBundle(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	var bundle gateway.ProfileBundle
	if failure := decodeJSON(writer, request, maximumBundleBodyBytes, &bundle); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	states, err := api.resources.ReplaceBundle(request.Context(), mustPrincipal(request.Context()).OwnerID, bundle)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"profiles": states, "defaults": map[string]any{"recoveryPolicy": hardenllm.DefaultStructuredRecoveryPolicy()}}, map[string]any{})
}

func (api *API) listHistory(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	_, pagePresent := query["page"]
	_, cursorPresent := query["cursor"]
	if pagePresent && cursorPresent {
		writeError(writer, http.StatusBadRequest, "invalid_request", "History page and cursor cannot be combined.")
		return
	}
	limit := 0
	if rawLimit, present := query["limit"]; present && (pagePresent || rawLimit[0] != "") {
		parsed, err := strconv.ParseInt(rawLimit[0], 10, 64)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "The history limit is invalid.")
			return
		}
		if parsed < 1 || parsed > maximumHistoryPageSize {
			writeError(writer, http.StatusBadRequest, "invalid_request", "The history limit is invalid.")
			return
		}
		limit = int(parsed)
	}
	var parsedPage int64
	if pagePresent {
		var err error
		parsedPage, err = strconv.ParseInt(query["page"][0], 10, 64)
		if err != nil || parsedPage < 1 {
			writeError(writer, http.StatusBadRequest, "invalid_request", "The history page is invalid.")
			return
		}
	}
	if !api.requireResources(writer) {
		return
	}
	if pagePresent {
		page, err := api.resources.NumberedHistory(request.Context(), mustPrincipal(request.Context()).OwnerID, parsedPage, limitOrDefault(limit))
		if err != nil {
			api.writeServiceError(writer, err)
			return
		}
		writeSuccess(writer, http.StatusOK, page, map[string]any{})
		return
	}
	page, err := api.resources.History(request.Context(), mustPrincipal(request.Context()).OwnerID, query.Get("cursor"), limit)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, page, map[string]any{})
}

func limitOrDefault(limit int) int {
	if limit == 0 {
		return defaultHistoryPageSize
	}
	return limit
}

func (api *API) getStats(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	stats, err := api.resources.Stats(request.Context(), mustPrincipal(request.Context()).OwnerID)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, stats, map[string]any{})
}

func (api *API) deleteHistory(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	runID := chi.URLParam(request, "historyID")
	if err := api.resources.DeleteHistory(request.Context(), mustPrincipal(request.Context()).OwnerID, runID); err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"deleted": true, "runId": runID}, map[string]any{})
}

func (api *API) clearHistory(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	count, err := api.resources.ClearHistory(request.Context(), mustPrincipal(request.Context()).OwnerID)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"deletedCount": count}, map[string]any{})
}

func (api *API) getTrace(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	trace, err := api.resources.Trace(request.Context(), mustPrincipal(request.Context()).OwnerID, chi.URLParam(request, "traceID"))
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, trace, map[string]any{})
}

func (api *API) getArtifact(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	location, err := api.resources.PresignArtifact(
		request.Context(), mustPrincipal(request.Context()).OwnerID,
		chi.URLParam(request, "traceID"), chi.URLParam(request, "artifactID"),
	)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Location", location)
	writer.WriteHeader(http.StatusSeeOther)
}

func (api *API) run(writer http.ResponseWriter, request *http.Request) {
	if api.runs == nil {
		api.notImplemented(writer, request)
		return
	}
	var input gateway.RunInput
	if failure := decodeJSON(writer, request, maximumRunBodyBytes, &input); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	if strings.TrimSpace(request.Header.Get("Last-Event-ID")) != "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "SSE resume is not supported for request-bound runs.")
		return
	}
	duration := api.maxRunDuration
	if input.TimeoutMS > 0 {
		requested := time.Duration(input.TimeoutMS) * time.Millisecond
		if requested > api.maxRunDuration {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "The requested run timeout exceeds the deployment maximum.")
			return
		}
		duration = requested
	}
	if acceptsEventStream(request) {
		api.runSSE(writer, request, input, duration)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), duration)
	defer cancel()
	result, state, err := api.runs.Run(ctx, mustPrincipal(request.Context()).OwnerID, input)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			writeErrorState(writer, http.StatusGatewayTimeout, "run_timeout", "The run exceeded its deadline.", state)
			return
		}
		if failure := requestValidationFailure(err); failure != nil {
			writeFailure(writer, *failure)
			return
		}
		if errors.Is(err, gateway.ErrInvalidRequest) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "The run request is invalid.")
			return
		}
		if errors.Is(err, gateway.ErrCredentialNotConfigured) {
			writeErrorState(writer, http.StatusUnprocessableEntity, "credential_required", "The selected profile has no configured endpoint credential.", state)
			return
		}
		if errors.Is(err, postgres.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "not_found", "The selected profile was not found.")
			return
		}
		writeErrorState(writer, http.StatusBadGateway, "run_failed", "The provider run failed.", state)
		return
	}
	writeSuccess(writer, http.StatusOK, result, state)
}

type runOutcome struct {
	result gateway.RunOutput
	state  gateway.RunState
	err    error
}

type streamEnvelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	Sequence      uint64 `json:"sequence"`
	RunID         string `json:"runId,omitempty"`
	CallID        string `json:"callId,omitempty"`
	TraceID       string `json:"traceId,omitempty"`
	Type          string `json:"type"`
	Data          any    `json:"data"`
}

func acceptsEventStream(request *http.Request) bool {
	for _, value := range strings.Split(request.Header.Get("Accept"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]), "text/event-stream") {
			return true
		}
	}
	return false
}

func (api *API) runSSE(writer http.ResponseWriter, request *http.Request, input gateway.RunInput, duration time.Duration) {
	flusher, canFlush := writer.(http.Flusher)
	if !canFlush {
		writeError(writer, http.StatusNotImplemented, "streaming_unavailable", "The server cannot stream progress events.")
		return
	}
	progress := make(chan hardenllm.ProgressEvent, 32)
	ready := make(chan struct{}, 1)
	input.Progress = progress
	input.Ready = ready
	ctx, cancel := context.WithTimeout(request.Context(), duration)
	defer cancel()
	outcomes := make(chan runOutcome, 1)
	// Keep the channel passed to Run stable in the worker closure. The stream
	// loop deliberately sets its local progress variable to nil after draining
	// the channel; capturing that mutable variable here would make a fast run
	// close nil and panic under the race detector.
	go func(progressChannel chan hardenllm.ProgressEvent) {
		result, state, err := api.runs.Run(ctx, mustPrincipal(request.Context()).OwnerID, input)
		outcomes <- runOutcome{result: result, state: state, err: err}
		close(progressChannel)
	}(progress)
	var outcome runOutcome
	select {
	case <-ready:
		// Static admission succeeded. Only now commit the SSE representation.
	case outcome = <-outcomes:
		// A very fast run can make both the admission signal and the outcome
		// ready at once. Prefer the signal if Run already admitted the call;
		// otherwise validation/catalog/initialization failures retain the normal
		// JSON transport because no stream contract was accepted.
		select {
		case <-ready:
			// Continue with the request-bound stream and publish the outcome below.
		default:
			api.writeRunError(writer, ctx, outcome)
			return
		}
	case <-request.Context().Done():
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	var sequence uint64
	lastRunID := ""
	lastCallID := ""
	lastTraceID := ""
	writeEvent := func(eventType string, runID, callID, traceID string, data any) bool {
		sequence++
		if runID != "" {
			lastRunID = runID
		}
		if callID != "" {
			lastCallID = callID
		}
		if traceID != "" {
			lastTraceID = traceID
		}
		envelope := streamEnvelope{SchemaVersion: 1, Sequence: sequence, RunID: lastRunID, CallID: lastCallID, TraceID: lastTraceID, Type: eventType, Data: data}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return false
		}
		if _, err := writer.Write([]byte("id: " + strconv.FormatUint(sequence, 10) + "\nevent: " + eventType + "\ndata: " + string(encoded) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-progress:
			if !ok {
				progress = nil
				continue
			}
			if event.Type == "run.terminal" {
				continue
			}
			if !writeEvent(event.Type, event.RunID, event.CallID, event.TraceID, event) {
				return
			}
		case outcome := <-outcomes:
			// Drain snapshots that were already produced before publishing the
			// terminal event. Dropped snapshots remain legal; the final result is
			// authoritative.
			for progress != nil {
				select {
				case event, ok := <-progress:
					if !ok {
						progress = nil
						continue
					}
					if event.Type == "run.terminal" {
						continue
					}
					if !writeEvent(event.Type, event.RunID, event.CallID, event.TraceID, event) {
						return
					}
				default:
					progress = nil
				}
			}
			if outcome.err == nil {
				writeEvent("run.completed", outcome.state.LastRunID, outcome.result.CallID, outcome.result.TraceID, envelopeForOutcome(outcome))
			} else {
				writeEvent("run.failed", outcome.state.LastRunID, outcome.result.CallID, outcome.result.TraceID, envelopeForOutcome(outcome))
			}
			return
		case <-ticker.C:
			if _, err := writer.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func (api *API) writeRunError(writer http.ResponseWriter, executionContext context.Context, outcome runOutcome) {
	if errors.Is(executionContext.Err(), context.DeadlineExceeded) || errors.Is(outcome.err, context.DeadlineExceeded) {
		writeErrorState(writer, http.StatusGatewayTimeout, "run_timeout", "The run exceeded its deadline.", outcome.state)
		return
	}
	if failure := requestValidationFailure(outcome.err); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	if errors.Is(outcome.err, gateway.ErrInvalidRequest) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "The run request is invalid.")
		return
	}
	if errors.Is(outcome.err, gateway.ErrCredentialNotConfigured) {
		writeErrorState(writer, http.StatusUnprocessableEntity, "credential_required", "The selected profile has no configured endpoint credential.", outcome.state)
		return
	}
	if errors.Is(outcome.err, postgres.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "The selected profile was not found.")
		return
	}
	writeErrorState(writer, http.StatusBadGateway, "run_failed", "The provider run failed.", outcome.state)
}

func envelopeForOutcome(outcome runOutcome) envelope {
	if outcome.err == nil {
		return envelope{State: outcome.state, Result: outcome.result}
	}
	code, message := "run_failed", "The provider run failed."
	if errors.Is(outcome.err, context.DeadlineExceeded) {
		code, message = "run_timeout", "The run exceeded its deadline."
	}
	return envelope{State: outcome.state, Result: outcome.result, Error: &Error{Code: code, Message: message}}
}

func (api *API) requireResources(writer http.ResponseWriter) bool {
	if api.resources == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "The requested operation is temporarily unavailable.")
		return false
	}
	return true
}

func (api *API) writeServiceError(writer http.ResponseWriter, err error) {
	if failure := requestValidationFailure(err); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, gateway.ErrInvalidCursor), errors.Is(err, gateway.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_request", "The request is invalid.")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusGatewayTimeout, "operation_timeout", "The operation exceeded its deadline.")
	default:
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "The operation is temporarily unavailable.")
	}
}

func mustPrincipal(ctx context.Context) authPrincipal {
	principal, _ := principalFrom(ctx)
	return authPrincipal{OwnerID: principal.OwnerID}
}

type authPrincipal struct{ OwnerID string }

func writeErrorState(writer http.ResponseWriter, status int, code, message string, state any) {
	writeJSON(writer, status, envelope{State: state, Result: nil, Error: &Error{Code: code, Message: message}})
}

func requestValidationFailure(err error) *responseFailure {
	var policyError *hardenllm.RecoveryPolicyError
	var profileError *profiles.ValidationError
	fields := map[string]string{}
	switch {
	case errors.As(err, &policyError):
		fields[policyError.Field] = policyError.Message
	case errors.As(err, &profileError):
		for _, field := range profileError.FieldErrors {
			fields[field.Field] = field.Message
		}
	default:
		return nil
	}
	return &responseFailure{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Settings validation failed.", FieldErrors: fields}
}
