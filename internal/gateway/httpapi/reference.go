package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

const maximumBundleBytes = 2 << 20
const maximumHistoryResponseBytes = (17 << 20) + maximumHardenBytes

type referenceErrorEnvelope struct {
	Error *stateful.RunError `json:"error"`
}
type referenceRunEnvelope struct {
	Result stateful.RunOutput `json:"result"`
	State  stateful.RunState  `json:"state"`
}

func referenceErrorStatus(err error) int {
	var operation *stateful.OperationError
	var validation *stateful.ValidationError
	var core *hardenllm.ValidationError
	if errors.As(err, &validation) || errors.As(err, &core) {
		return http.StatusBadRequest
	}
	if errors.As(err, &operation) {
		switch operation.Code {
		case "invalid_api_key", "unauthorized":
			return http.StatusUnauthorized
		case "scope_selector_rejected", "invalid_request", "profile_invalid", "credential_invalid", "draft_invalid", "history_query_invalid", "artifact_invalid":
			return http.StatusBadRequest
		case "profile_not_found", "preset_not_found", "history_not_found", "artifact_not_found":
			return http.StatusNotFound
		case "revision_conflict":
			return http.StatusConflict
		case "request_too_large":
			return http.StatusRequestEntityTooLarge
		case "unsupported_media_type":
			return http.StatusUnsupportedMediaType
		case "storage_unavailable", "reference_configuration_error", "history_configuration_error", "history_read_failed":
			return http.StatusServiceUnavailable
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	return http.StatusBadGateway
}

func writeReferenceFailure(w http.ResponseWriter, status int, problem *stateful.RunError) {
	writeJSON(w, status, referenceErrorEnvelope{Error: problem}, maximumHardenBytes)
}
func (api *API) referenceFailure(w http.ResponseWriter, r *http.Request, err error) {
	problem := stateful.ProjectRunError(err)
	writeReferenceFailure(w, referenceErrorStatus(err), problem)
	api.logger.WarnContext(r.Context(), "reference operation failed", "code", problem.Code, "route", r.Pattern)
}
func writeProtocolError(w http.ResponseWriter, r *http.Request, status int, typeName, code, message string, param any) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		writeReferenceFailure(w, status, &stateful.RunError{Code: code, Message: message})
		return
	}
	writeError(w, status, typeName, code, message, param)
}

func referenceQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for key, values := range r.URL.Query() {
		known := false
		for _, candidate := range allowed {
			known = known || key == candidate
		}
		if !known || len(values) != 1 {
			writeReferenceFailure(w, 400, &stateful.RunError{Code: "invalid_request", Message: "The operation has an unsupported or repeated query parameter."})
			return false
		}
	}
	return true
}

func decodeReference(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeReferenceFailure(w, 415, &stateful.RunError{Code: "unsupported_media_type", Message: "Content-Type must be application/json."})
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maximumRequestBytes+1))
	if err != nil {
		writeReferenceFailure(w, 400, &stateful.RunError{Code: "invalid_request", Message: "The JSON request could not be read."})
		return false
	}
	if len(data) > maximumRequestBytes {
		writeReferenceFailure(w, 413, &stateful.RunError{Code: "request_too_large", Message: "The request exceeds its 256 KiB size limit."})
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		writeReferenceFailure(w, 400, &stateful.RunError{Code: "invalid_request", Message: "The request has malformed or unsupported fields."})
		return false
	}
	return true
}

func (api *API) referenceStatus(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	status, err := api.service.Status(r.Context(), scopeFromContext(r.Context()).hash)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, status, maximumHardenBytes)
}
func (api *API) referenceProfiles(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	hash := scopeFromContext(r.Context()).hash
	if r.Method == http.MethodGet {
		profiles, err := api.service.ListProfiles(r.Context(), hash)
		if err != nil {
			api.referenceFailure(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profiles": profiles}, maximumResponseBytes)
		return
	}
	var input stateful.SaveProfileInput
	if !decodeReference(w, r, &input) {
		return
	}
	profile, err := api.service.SaveProfile(r.Context(), hash, input)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"profile": profile}, maximumResponseBytes)
}
func (api *API) referenceProfile(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	hash, id := scopeFromContext(r.Context()).hash, r.PathValue("profileID")
	if r.Method == http.MethodDelete {
		if err := api.service.DeleteProfile(r.Context(), hash, id); err != nil {
			api.referenceFailure(w, r, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	profile, err := api.service.GetProfile(r.Context(), hash, id)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, profile, maximumResponseBytes)
}
func (api *API) referenceModels(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	profile, err := api.service.RefreshModels(r.Context(), scopeFromContext(r.Context()).hash, r.PathValue("profileID"))
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, profile, maximumResponseBytes)
}
func (api *API) referenceBundle(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	hash := scopeFromContext(r.Context()).hash
	if r.Method == http.MethodGet {
		data, err := api.service.ExportBundle(r.Context(), hash)
		if err != nil {
			api.referenceFailure(w, r, err)
			return
		}
		writeJSON(w, 200, json.RawMessage(data), maximumBundleBytes)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeReferenceFailure(w, 415, &stateful.RunError{Code: "unsupported_media_type", Message: "Content-Type must be application/json."})
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maximumBundleBytes+1))
	if err != nil || len(data) > maximumBundleBytes {
		writeReferenceFailure(w, 413, &stateful.RunError{Code: "request_too_large", Message: "Profile bundle exceeds its 2 MiB size limit."})
		return
	}
	if err = api.service.ReplaceBundle(r.Context(), hash, data); err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	profiles, err := api.service.ListProfiles(r.Context(), hash)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"profiles": profiles}, maximumResponseBytes)
}
func (api *API) referenceState(w http.ResponseWriter, r *http.Request) {
	hash := scopeFromContext(r.Context()).hash
	var draft stateful.Draft
	var err error
	if r.Method == http.MethodGet {
		if !referenceQuery(w, r, "draftKey") {
			return
		}
		draft, err = api.service.GetDraft(r.Context(), hash, r.URL.Query().Get("draftKey"))
	} else {
		if !referenceQuery(w, r) {
			return
		}
		var input struct {
			DraftKey string          `json:"draftKey"`
			Revision int64           `json:"revision"`
			Payload  json.RawMessage `json:"payload"`
		}
		if !decodeReference(w, r, &input) {
			return
		}
		draft, err = api.service.SaveDraft(r.Context(), hash, input.DraftKey, stateful.SaveDraftInput{Revision: input.Revision, Payload: input.Payload})
	}
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, draft, maximumRequestBytes+maximumHardenBytes)
}

func referenceHistoryQuery(r *http.Request, paging bool) (stateful.HistoryQuery, error) {
	q := stateful.HistoryQuery{}
	failure := func() (stateful.HistoryQuery, error) {
		return q, &stateful.OperationError{Code: "history_query_invalid", Message: "Use valid UTC dates, a positive page and page size 10, 25, 50 or 100."}
	}
	for _, field := range []string{"from", "before"} {
		if values, ok := r.URL.Query()[field]; ok {
			if len(values) != 1 {
				return failure()
			}
			value, err := time.Parse(time.RFC3339Nano, values[0])
			if err != nil {
				return failure()
			}
			if field == "from" {
				q.From = &value
			} else {
				q.Before = value
			}
		}
	}
	if paging {
		for _, field := range []string{"page", "pageSize"} {
			if values, ok := r.URL.Query()[field]; ok {
				if len(values) != 1 {
					return failure()
				}
				value, err := strconv.Atoi(values[0])
				if err != nil || value <= 0 {
					return failure()
				}
				if field == "page" {
					q.Page = value
				} else {
					q.PageSize = value
				}
			}
		}
	}
	return q, nil
}
func (api *API) referenceHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !referenceQuery(w, r, "from", "before", "page", "pageSize") {
			return
		}
	} else if !referenceQuery(w, r, "from", "before") {
		return
	}
	q, err := referenceHistoryQuery(r, r.Method == http.MethodGet)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	hash := scopeFromContext(r.Context()).hash
	if r.Method == http.MethodDelete {
		result, err := api.service.ClearHistory(r.Context(), hash, q)
		if err != nil && result.FailedRunID == "" {
			api.referenceFailure(w, r, err)
			return
		}
		writeJSON(w, 200, result, maximumHardenBytes)
		return
	}
	page, err := api.service.ListHistory(r.Context(), hash, q)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, page, maximumResponseBytes)
}
func (api *API) referenceHistoryEntry(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	hash, id := scopeFromContext(r.Context()).hash, r.PathValue("runID")
	if r.Method == http.MethodDelete {
		if err := api.service.DeleteHistory(r.Context(), hash, id); err != nil {
			api.referenceFailure(w, r, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	record, err := api.service.GetHistory(r.Context(), hash, id)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, record, maximumHistoryResponseBytes)
}
func (api *API) referenceStats(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r, "from", "before") {
		return
	}
	q, err := referenceHistoryQuery(r, false)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	stats, err := api.service.Statistics(r.Context(), scopeFromContext(r.Context()).hash, q)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, stats, maximumHardenBytes)
}
func (api *API) referenceArtifact(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	id := r.PathValue("artifactID")
	if id == "" {
		id = "trace.json"
	}
	artifact, err := api.service.OpenArtifact(r.Context(), scopeFromContext(r.Context()).hash, r.PathValue("runID"), id)
	if err != nil {
		api.referenceFailure(w, r, err)
		return
	}
	defer artifact.Body.Close()
	w.Header().Set("Content-Type", artifact.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	w.Header().Set("ETag", `"`+artifact.SHA256+`"`)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": id}))
	w.WriteHeader(200)
	_, _ = io.Copy(w, artifact.Body)
}
func (api *API) referenceRun(w http.ResponseWriter, r *http.Request) {
	if !referenceQuery(w, r) {
		return
	}
	var input stateful.RunInput
	if !decodeReference(w, r, &input) {
		return
	}
	if r.Header.Get("Last-Event-ID") != "" {
		writeReferenceFailure(w, 400, &stateful.RunError{Code: "invalid_request", Message: "Request-bound inference cannot be resumed or replayed."})
		return
	}
	if input.TimeoutMS < 0 || time.Duration(input.TimeoutMS)*time.Millisecond > api.maxRunDuration {
		writeReferenceFailure(w, 400, &stateful.RunError{Code: "invalid_request", Message: "The requested timeout exceeds the deployment maximum."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.maxRunDuration)
	defer cancel()
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		api.referenceRunSSE(w, r, input, ctx)
		return
	}
	out, state, err := api.service.Run(ctx, scopeFromContext(ctx).hash, input)
	api.logRecording(ctx, out)
	if err != nil {
		api.referenceRunFailure(w, r, err, out)
		return
	}
	writeJSON(w, 200, referenceRunEnvelope{Result: out, State: state}, maximumResponseBytes)
}

func (api *API) referenceRunFailure(w http.ResponseWriter, r *http.Request, err error, out stateful.RunOutput) {
	problem := stateful.ProjectRunError(err)
	problem.Diagnostic = &stateful.Diagnostic{Code: problem.Code, Component: "reference", Operation: "run", RequestID: out.RunID, RunID: out.RunID, TraceID: out.TraceID, InferenceOutcome: out.Status, PersistenceOutcome: out.History.Status, Cause: "rejected", Message: problem.Message, Action: "Correct the indicated configuration or inspect this run's trace; do not replay an ambiguous inference.", DurationMS: out.TotalCallDurationMs}
	api.logger.InfoContext(r.Context(), "reference execution rejected", "code", problem.Code, "run_id", out.RunID, "trace_id", out.TraceID, "provider_invoked", out.ProviderInvoked)
	writeReferenceFailure(w, referenceErrorStatus(err), problem)
}

func (api *API) logRecording(ctx context.Context, out stateful.RunOutput) {
	for _, diagnostic := range []*stateful.Diagnostic{out.ArtifactDiagnostic, out.History.Diagnostic} {
		if diagnostic != nil {
			api.logger.WarnContext(ctx, "reference persistence diagnostic", "code", diagnostic.Code, "component", diagnostic.Component, "operation", diagnostic.Operation, "request_id", diagnostic.RequestID, "run_id", diagnostic.RunID, "trace_id", diagnostic.TraceID, "inference_outcome", diagnostic.InferenceOutcome, "persistence_outcome", diagnostic.PersistenceOutcome, "cause", diagnostic.Cause, "duration_ms", diagnostic.DurationMS, "message", diagnostic.Message, "action", diagnostic.Action)
		}
	}
}
