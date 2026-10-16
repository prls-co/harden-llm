package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful/batch"
)

const maximumJobRequestBytes = 32 << 20

type BatchService interface {
	UpstreamID() string
	Submit(context.Context, string, string, []batch.Item) (batch.Status, error)
	Status(context.Context, string, string) (batch.Status, error)
	WriteResults(context.Context, string, string, io.Writer) error
}
type createJobRequest struct {
	Items []struct {
		CustomID string          `json:"custom_id"`
		Request  json.RawMessage `json:"request"`
	} `json:"items"`
}

func (api *API) createJob(w http.ResponseWriter, r *http.Request) {
	if api.jobs == nil {
		writeError(w, 503, "server_error", "jobs_unavailable", "Durable batch execution is not configured.", nil)
		return
	}
	key := r.Header.Values("Idempotency-Key")
	if len(key) != 1 || len(key[0]) < 1 || len(key[0]) > 128 {
		writeRequestError(w, errors.New("One Idempotency-Key header of 1 through 128 characters is required."), "Idempotency-Key")
		return
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeError(w, 415, "invalid_request_error", "unsupported_media_type", "Content-Type must be application/json.", nil)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximumJobRequestBytes))
	decoder.DisallowUnknownFields()
	var input createJobRequest
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		status := 400
		if errors.As(err, &tooLarge) {
			status = 413
		}
		writeError(w, status, "invalid_request_error", "invalid_request", "The batch manifest contains malformed, oversized, or unsupported fields.", nil)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeRequestError(w, errors.New("One JSON object is required."), "items")
		return
	}
	if len(input.Items) < 1 || len(input.Items) > batch.MaximumItems {
		writeRequestError(w, errors.New("items must contain from 1 through 1000 entries."), "items")
		return
	}
	items := make([]batch.Item, 0, len(input.Items))
	seen := map[string]bool{}
	for _, v := range input.Items {
		if len(v.CustomID) < 1 || len(v.CustomID) > 128 || seen[v.CustomID] {
			writeRequestError(w, errors.New("custom_id values must be distinct and contain 1 through 128 characters."), "custom_id")
			return
		}
		seen[v.CustomID] = true
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://internal/v1/responses", bytes.NewReader(v.Request))
		req.Header.Set("Content-Type", "application/json")
		var native responsesRequest
		if !api.decodeRequest(w, req, &native) {
			return
		}
		if native.Model != "gpt-6-luna" {
			writeRequestError(w, errors.New("The batch route currently supports gpt-6-luna."), "model")
			return
		}
		if native.Harden.Upstream != "" && native.Harden.Upstream != api.jobs.UpstreamID() {
			writeRequestError(w, errors.New("The batch route requires its configured CPA upstream."), "harden.upstream")
			return
		}
		if native.Harden.Cache != "" && native.Harden.Cache != hardenllm.CacheModeOff {
			writeRequestError(w, errors.New("Batch response caching must be off."), "harden.cache")
			return
		}
		if p := native.Harden.Recovery; p != nil && (p.MaxAttempts != 1 || p.JSONRepair != nil || p.Rerun != nil) {
			writeRequestError(w, errors.New("Batch items support one generation without repair or recovery."), "harden.recovery")
			return
		}
		timeout := native.Harden.TimeoutMS
		if timeout == 0 {
			timeout = 180000
		}
		if timeout < 1 || timeout > 180000 {
			writeRequestError(w, errors.New("Batch timeout_ms must be from 1 through 180000."), "harden.timeout_ms")
			return
		}
		native.Harden.TimeoutMS = 0
		p := batch.SingleAttemptPolicy()
		native.Harden.Recovery = &p
		prepared, ok := prepareResponses(w, native)
		if !ok {
			return
		}
		call := prepared.Request
		if call.ReasoningEffort == "" {
			call.ReasoningEffort = "low"
		}
		if call.ReasoningEffort != "low" || call.WebSearch {
			writeRequestError(w, errors.New("Batch v1 requires reasoning low and no web-search execution."), "reasoning")
			return
		}
		if call.CallType == hardenllm.CallTypeStructured {
			normalized, err := hardenllm.NormalizeSchema(call.Schema)
			if err != nil {
				writeRequestError(w, errors.New("The output schema is invalid."), "text")
				return
			}
			call.Schema = normalized
		}
		call.ProviderOptions["stream"] = true
		parallel := true
		if native.ParallelToolCalls != nil {
			parallel = *native.ParallelToolCalls
		}
		items = append(items, batch.Item{CustomID: v.CustomID, Generation: batch.Generation{ConnectionID: api.jobs.UpstreamID(), ModelID: call.ModelID, Messages: call.Messages, CallType: call.CallType, Schema: call.Schema, Tools: call.Tools, ToolChoice: call.ToolChoice, ReasoningEffort: call.ReasoningEffort, ProviderOptions: call.ProviderOptions, ParallelToolCalls: parallel, TimeoutMS: timeout}})
	}
	status, err := api.jobs.Submit(r.Context(), scopeFromContext(r.Context()).hash, key[0], items)
	if err != nil {
		api.jobError(w, err)
		return
	}
	w.Header().Set("Location", status.StatusURL)
	jobHeaders(w)
	writeJSON(w, 202, status, maximumResponseBytes)
}
func jobHeaders(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Cache-Control", "no-store")
}
func (api *API) jobError(w http.ResponseWriter, err error) {
	status, code := 503, "jobs_unavailable"
	if errors.Is(err, batch.ErrNotFound) {
		status, code = 404, "not_found"
	}
	if errors.Is(err, batch.ErrConflict) {
		status, code = 409, "idempotency_conflict"
	}
	writeError(w, status, "server_error", code, "The durable job operation could not be completed.", nil)
}
func (api *API) jobStatus(w http.ResponseWriter, r *http.Request) {
	if api.jobs == nil {
		api.jobError(w, batch.ErrUnavailable)
		return
	}
	v, err := api.jobs.Status(r.Context(), scopeFromContext(r.Context()).hash, r.PathValue("jobID"))
	if err != nil {
		api.jobError(w, err)
		return
	}
	jobHeaders(w)
	writeJSON(w, 200, v, maximumResponseBytes)
}
func (api *API) jobResult(w http.ResponseWriter, r *http.Request) {
	if api.jobs == nil {
		api.jobError(w, batch.ErrUnavailable)
		return
	}
	scope := scopeFromContext(r.Context()).hash
	id := r.PathValue("jobID")
	v, err := api.jobs.Status(r.Context(), scope, id)
	if err != nil {
		api.jobError(w, err)
		return
	}
	jobHeaders(w)
	if !v.Counts.Done() {
		writeJSON(w, 202, v, maximumResponseBytes)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Del("Retry-After")
	if err = api.jobs.WriteResults(r.Context(), scope, id, w); err != nil {
		if api.logger != nil {
			api.logger.ErrorContext(r.Context(), "batch result delivery interrupted", "job_id", id, "code", "result_delivery_failed")
		}
		panic(http.ErrAbortHandler)
	}
}

// EncodeBatchResponse reuses the ordinary Responses output contract in workers.
func EncodeBatchResponse(g batch.Generation, result hardenllm.Result, at time.Time) (json.RawMessage, error) {
	v, err := responsesResponse(g.ModelID, result, at.Unix(), true, g.ParallelToolCalls)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
