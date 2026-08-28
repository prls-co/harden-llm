package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

const (
	maximumProfileBodyBytes = 256 << 10
	maximumBundleBodyBytes  = 2 << 20
	maximumRunBodyBytes     = 256 << 10
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
	writeSuccess(writer, http.StatusOK, map[string]any{"profiles": states}, map[string]any{})
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
	writeSuccess(writer, http.StatusOK, map[string]any{"profiles": states}, map[string]any{})
}

func (api *API) listHistory(writer http.ResponseWriter, request *http.Request) {
	if !api.requireResources(writer) {
		return
	}
	limit := 0
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "The history limit is invalid.")
			return
		}
		limit = parsed
	}
	page, err := api.resources.History(request.Context(), mustPrincipal(request.Context()).OwnerID, request.URL.Query().Get("cursor"), limit)
	if err != nil {
		api.writeServiceError(writer, err)
		return
	}
	writeSuccess(writer, http.StatusOK, page, map[string]any{})
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
	duration := api.maxRunDuration
	if input.TimeoutMS > 0 {
		requested := time.Duration(input.TimeoutMS) * time.Millisecond
		if requested > api.maxRunDuration {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_request", "The requested run timeout exceeds the deployment maximum.")
			return
		}
		duration = requested
	}
	ctx, cancel := context.WithTimeout(request.Context(), duration)
	defer cancel()
	result, state, err := api.runs.Run(ctx, mustPrincipal(request.Context()).OwnerID, input)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			writeErrorState(writer, http.StatusGatewayTimeout, "run_timeout", "The run exceeded its deadline.", state)
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

func (api *API) requireResources(writer http.ResponseWriter) bool {
	if api.resources == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "The requested operation is temporarily unavailable.")
		return false
	}
	return true
}

func (api *API) writeServiceError(writer http.ResponseWriter, err error) {
	var validation *profiles.ValidationError
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, gateway.ErrInvalidCursor), errors.Is(err, gateway.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_request", "The request is invalid.")
	case errors.Is(err, gateway.ErrProfileConflict):
		writeError(writer, http.StatusConflict, "profile_conflict", "The profile is still referenced.")
	case errors.As(err, &validation):
		fields := make(map[string]string, len(validation.FieldErrors))
		for _, field := range validation.FieldErrors {
			fields[field.Field] = field.Message
		}
		writeJSON(writer, http.StatusUnprocessableEntity, envelope{State: map[string]any{}, Error: &Error{Code: "validation_failed", Message: "Profile validation failed.", FieldErrors: fields}})
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
