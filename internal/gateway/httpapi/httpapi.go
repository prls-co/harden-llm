// Package httpapi implements the frontend-independent Harden-LLM REST adapter.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"go.opentelemetry.io/otel/propagation"
)

const (
	maximumJSONBodyBytes    = 64 << 10
	readinessTimeout        = 2 * time.Second
	maximumRunDuration      = 60 * time.Second
	defaultOperationTimeout = 15 * time.Second
	maximumOperationTimeout = 30 * time.Second
)

type ReadinessCheck func(context.Context) error

type IdentityService interface {
	Login(context.Context, string, string) (auth.LoginResult, error)
	AuthenticateHeader(context.Context, []string) (auth.Principal, error)
	LogoutPrincipal(context.Context, auth.Principal) error
}

type Config struct {
	Auth             IdentityService
	Readiness        []ReadinessCheck
	Resources        *gateway.ResourceService
	Runs             *gateway.RunService
	MaxRunDuration   time.Duration
	OperationTimeout time.Duration
	Telemetry        *gateway.Telemetry
	Logger           *slog.Logger
}

type API struct {
	auth             IdentityService
	readiness        []ReadinessCheck
	resources        *gateway.ResourceService
	runs             *gateway.RunService
	maxRunDuration   time.Duration
	operationTimeout time.Duration
	telemetry        *gateway.Telemetry
	propagator       propagation.TextMapPropagator
	logger           *slog.Logger
	handler          http.Handler
}

type Error struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
}

type envelope struct {
	State  any    `json:"state"`
	Result any    `json:"result"`
	Error  *Error `json:"error"`
}

type principalContextKey struct{}

func New(config Config) (*API, error) {
	if config.Auth == nil {
		return nil, errors.New("httpapi: identity service is required")
	}
	for _, check := range config.Readiness {
		if check == nil {
			return nil, errors.New("httpapi: readiness check is nil")
		}
	}
	if config.MaxRunDuration == 0 {
		config.MaxRunDuration = maximumRunDuration
	}
	if config.MaxRunDuration < time.Millisecond || config.MaxRunDuration > maximumRunDuration {
		return nil, errors.New("httpapi: maximum run duration is outside the supported range")
	}
	if config.OperationTimeout == 0 {
		config.OperationTimeout = defaultOperationTimeout
	}
	if config.OperationTimeout < time.Millisecond || config.OperationTimeout > maximumOperationTimeout {
		return nil, errors.New("httpapi: operation timeout is outside the supported range")
	}
	if config.Telemetry == nil {
		var err error
		config.Telemetry, err = gateway.NewTelemetry(nil, nil)
		if err != nil {
			return nil, errors.New("httpapi: initialize telemetry")
		}
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	api := &API{
		auth: config.Auth, readiness: append([]ReadinessCheck(nil), config.Readiness...),
		resources: config.Resources, runs: config.Runs, maxRunDuration: config.MaxRunDuration,
		operationTimeout: config.OperationTimeout,
		telemetry:        config.Telemetry,
		propagator:       propagation.TraceContext{},
		logger:           config.Logger,
	}
	api.handler = api.router()
	return api, nil
}

func (api *API) Handler() http.Handler {
	if api == nil || api.handler == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
		})
	}
	return api.handler
}

func (api *API) router() http.Handler {
	router := chi.NewRouter()
	router.Use(api.observeHTTP, api.recoverPanic, api.responsePolicy)
	router.NotFound(func(writer http.ResponseWriter, request *http.Request) {
		writeError(writer, http.StatusNotFound, "not_found", "The requested resource was not found.")
	})
	router.MethodNotAllowed(func(writer http.ResponseWriter, request *http.Request) {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not allowed.")
	})
	for _, route := range routeCatalog {
		handler := http.Handler(http.HandlerFunc(api.operationHandler(route.OperationID)))
		handler = api.validateRequestShape(route, handler)
		if route.Protected {
			handler = api.authenticate(handler)
		}
		if route.OperationID != "getHealth" && route.OperationID != "getReadiness" && route.OperationID != "run" {
			handler = api.withOperationTimeout(handler)
		}
		router.Method(route.Method, route.Path, handler)
	}
	return router
}

func (api *API) observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parentContext := api.propagator.Extract(
			request.Context(),
			propagation.HeaderCarrier(request.Header),
		)
		ctx, endRequest := api.telemetry.StartHTTP(parentContext, request.Method)
		statusWriter := &responseStatusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(statusWriter, request.WithContext(ctx))
		route := chi.RouteContext(request.Context()).RoutePattern()
		outcome, category := gateway.HTTPOutcome(statusWriter.status)
		api.logger.InfoContext(ctx, "http request completed",
			"method", request.Method, "route", route, "status", statusWriter.status,
			"outcome", outcome, "category", category,
		)
		endRequest(route, statusWriter.status)
	})
}

type responseStatusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *responseStatusWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseStatusWriter) Write(content []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(content)
}

func (writer *responseStatusWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (api *API) operationHandler(operationID string) http.HandlerFunc {
	switch operationID {
	case "getHealth":
		return api.health
	case "getReadiness":
		return api.ready
	case "login":
		return api.login
	case "logout":
		return api.logout
	case "getSession":
		return api.session
	case "getState":
		return api.getState
	case "saveState":
		return api.saveState
	case "listProfiles":
		return api.listProfiles
	case "exportProfileBundle":
		return api.exportProfileBundle
	case "importProfileBundle":
		return api.importProfileBundle
	case "saveProfile":
		return api.saveProfile
	case "deleteProfile":
		return api.deleteProfile
	case "refreshProfileModels":
		return api.refreshProfileModels
	case "listHistory":
		return api.listHistory
	case "deleteHistory":
		return api.deleteHistory
	case "clearHistory":
		return api.clearHistory
	case "getTrace":
		return api.getTrace
	case "getArtifact":
		return api.getArtifact
	case "run":
		return api.run
	default:
		return api.notImplemented
	}
}

func (api *API) responsePolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Pragma", "no-cache")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func (api *API) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := newBufferedResponse()
		defer func() {
			if recover() != nil {
				writeError(writer, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
				return
			}
			buffer.flush(writer)
		}()
		next.ServeHTTP(buffer, request)
	})
}

func (api *API) validateRequestShape(route Route, next http.Handler) http.Handler {
	allowedQuery := make(map[string]struct{}, len(route.QueryParameters))
	for _, name := range route.QueryParameters {
		allowedQuery[name] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "The request query is invalid.")
			return
		}
		for name, values := range query {
			if _, ok := allowedQuery[name]; !ok || len(values) != 1 {
				writeError(writer, http.StatusBadRequest, "invalid_request", "The request query is invalid.")
				return
			}
		}
		if !route.RequestBody {
			if request.ContentLength > 0 {
				writeError(writer, http.StatusBadRequest, "invalid_request", "This operation does not accept a request body.")
				return
			}
			if request.ContentLength < 0 {
				var one [1]byte
				count, readErr := request.Body.Read(one[:])
				if count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
					writeError(writer, http.StatusBadRequest, "invalid_request", "This operation does not accept a request body.")
					return
				}
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (api *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authContext, endAuth := api.telemetry.StartOperation(request.Context(), gateway.OperationAuthAuthenticate)
		principal, err := api.auth.AuthenticateHeader(authContext, request.Header.Values("Authorization"))
		endAuth(err)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				writeError(writer, http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
			} else {
				writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Authentication is temporarily unavailable.")
			}
			return
		}
		ctx := context.WithValue(request.Context(), principalContextKey{}, principal)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (api *API) withOperationTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), api.operationTimeout)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (api *API) health(writer http.ResponseWriter, _ *http.Request) {
	writeHealth(writer, http.StatusOK, "ok")
}

func (api *API) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
	defer cancel()
	for _, check := range api.readiness {
		if err := check(ctx); err != nil {
			writeHealth(writer, http.StatusServiceUnavailable, "unavailable")
			return
		}
	}
	writeHealth(writer, http.StatusOK, "ok")
}

func (api *API) login(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if failure := decodeJSON(writer, request, maximumJSONBodyBytes, &input); failure != nil {
		writeFailure(writer, *failure)
		return
	}
	if len(input.Email) == 0 || len(input.Email) > 320 || len(input.Password) < 12 || len(input.Password) > 1024 {
		writeError(writer, http.StatusBadRequest, "invalid_request", "Email and password are required.")
		return
	}
	authContext, endAuth := api.telemetry.StartOperation(request.Context(), gateway.OperationAuthLogin)
	result, err := api.auth.Login(authContext, input.Email, input.Password)
	endAuth(err)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "Authentication failed.")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Authentication is temporarily unavailable.")
		return
	}
	writeSuccess(writer, http.StatusOK, result, map[string]any{})
}

func (api *API) logout(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFrom(request.Context())
	authContext, endAuth := api.telemetry.StartOperation(request.Context(), gateway.OperationAuthLogout)
	var logoutErr error
	if ok {
		logoutErr = api.auth.LogoutPrincipal(authContext, principal)
	} else {
		logoutErr = auth.ErrUnauthenticated
	}
	endAuth(logoutErr)
	if logoutErr != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
		return
	}
	writeSuccess(writer, http.StatusOK, map[string]any{"revoked": true}, map[string]any{})
}

func (api *API) session(writer http.ResponseWriter, request *http.Request) {
	principal, ok := principalFrom(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
		return
	}
	writeSuccess(writer, http.StatusOK, principal, map[string]any{})
}

func (api *API) notImplemented(writer http.ResponseWriter, _ *http.Request) {
	writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "The requested operation is temporarily unavailable.")
}

func principalFrom(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, maximum int64, destination any) *responseFailure {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &responseFailure{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Content-Type must be application/json."}
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximum)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		var maximumError *http.MaxBytesError
		if errors.As(err, &maximumError) {
			return &responseFailure{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "The request body is too large."}
		}
		return &responseFailure{Status: http.StatusBadRequest, Code: "invalid_request", Message: "The request body is invalid."}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maximumError *http.MaxBytesError
		if errors.As(err, &maximumError) {
			return &responseFailure{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "The request body is too large."}
		}
		return &responseFailure{Status: http.StatusBadRequest, Code: "invalid_request", Message: "The request body must contain one JSON value."}
	}
	return nil
}

type responseFailure struct {
	Status  int
	Code    string
	Message string
}

func writeFailure(writer http.ResponseWriter, failure responseFailure) {
	writeError(writer, failure.Status, failure.Code, failure.Message)
}

func writeSuccess(writer http.ResponseWriter, status int, result, state any) {
	writeJSON(writer, status, envelope{State: state, Result: result})
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, envelope{State: map[string]any{}, Result: nil, Error: &Error{Code: code, Message: message}})
}

func writeHealth(writer http.ResponseWriter, status int, value string) {
	writeJSON(writer, status, map[string]string{"status": value})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(`{"state":{},"result":null,"error":{"code":"internal_error","message":"The response could not be encoded."}}`)
		status = http.StatusInternalServerError
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(append(encoded, '\n'))
}

type bufferedResponse struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), status: http.StatusOK}
}

func (response *bufferedResponse) Header() http.Header { return response.header }

func (response *bufferedResponse) WriteHeader(status int) {
	if response.wroteHeader || status < 100 {
		return
	}
	response.status = status
	response.wroteHeader = true
}

func (response *bufferedResponse) Write(data []byte) (int, error) {
	if !response.wroteHeader {
		response.WriteHeader(http.StatusOK)
	}
	return response.body.Write(data)
}

func (response *bufferedResponse) flush(writer http.ResponseWriter) {
	for name, values := range response.header {
		writer.Header()[name] = append([]string(nil), values...)
	}
	writer.WriteHeader(response.status)
	_, _ = writer.Write(response.body.Bytes())
}
