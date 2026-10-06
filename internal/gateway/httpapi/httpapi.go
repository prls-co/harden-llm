// Package httpapi exposes OpenAI-compatible, stateless inference endpoints.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"go.opentelemetry.io/otel/propagation"
)

const (
	MaximumRunDuration   = 60 * time.Second
	maximumRequestBytes  = 256 << 10
	maximumResponseBytes = 16 << 20
	maximumHardenBytes   = 64 << 10
	readinessTimeout     = 2 * time.Second
)

type ReadinessCheck func(context.Context) error

type Config struct {
	Token          string
	Client         *hardenllm.Client
	Readiness      []ReadinessCheck
	MaxRunDuration time.Duration
	Telemetry      *gateway.Telemetry
	Logger         *slog.Logger
}

type API struct {
	token          string
	client         *hardenllm.Client
	readiness      []ReadinessCheck
	maxRunDuration time.Duration
	telemetry      *gateway.Telemetry
	propagator     propagation.TextMapPropagator
	logger         *slog.Logger
	handler        http.Handler
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    string `json:"code"`
}

type errorResponse struct {
	Error  openAIError `json:"error"`
	Harden any         `json:"harden,omitempty"`
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.wrote {
		return
	}
	writer.wrote, writer.status = true, status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(data []byte) (int, error) {
	if !writer.wrote {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *statusWriter) Flush() {
	if !writer.wrote {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *statusWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func New(config Config) (*API, error) {
	if !auth.ValidToken(config.Token) {
		return nil, errors.New("httpapi: a bounded bearer token is required")
	}
	if config.Client == nil {
		return nil, errors.New("httpapi: the shared client is required")
	}
	for _, check := range config.Readiness {
		if check == nil {
			return nil, errors.New("httpapi: readiness check is nil")
		}
	}
	if config.MaxRunDuration == 0 {
		config.MaxRunDuration = MaximumRunDuration
	}
	if config.MaxRunDuration < time.Millisecond || config.MaxRunDuration > MaximumRunDuration {
		return nil, errors.New("httpapi: maximum run duration is outside the supported range")
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
		token: config.Token, client: config.Client, readiness: append([]ReadinessCheck(nil), config.Readiness...),
		maxRunDuration: config.MaxRunDuration, telemetry: config.Telemetry,
		propagator: propagation.TraceContext{}, logger: config.Logger,
	}
	api.handler = api.router()
	return api, nil
}

func (api *API) Handler() http.Handler {
	if api == nil || api.handler == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeError(writer, http.StatusServiceUnavailable, "server_error", "service_unavailable", "The service is unavailable.", nil)
		})
	}
	return api.handler
}

func (api *API) router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.ready)
	mux.HandleFunc("GET /v1/models", api.authorize(api.models))
	mux.HandleFunc("POST /v1/chat/completions", api.authorize(api.chatCompletions))
	mux.HandleFunc("POST /v1/responses", api.authorize(api.responses))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v1/") {
			writeError(writer, http.StatusNotFound, "invalid_request_error", "not_found", "The requested API route was not found.", nil)
			return
		}
		writeError(writer, http.StatusNotFound, "invalid_request_error", "not_found", "The requested route was not found.", nil)
	})
	return api.observeHTTP(mux)
}

func (api *API) observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := api.propagator.Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		ctx, endRequest := api.telemetry.StartHTTP(ctx, request.Method)
		wrapped := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(wrapped, request.WithContext(ctx))
		route := request.URL.Path
		switch route {
		case "/v1/chat/completions", "/v1/responses", "/v1/models", "/healthz", "/readyz":
		default:
			route = "unmatched"
		}
		outcome, category := gateway.HTTPOutcome(wrapped.status)
		api.logger.InfoContext(ctx, "http request completed", "method", request.Method, "route", route, "status", wrapped.status, "outcome", outcome, "category", category)
		endRequest(route, wrapped.status)
	})
}

func (api *API) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !auth.MatchBearer(request.Header.Values("Authorization"), api.token) {
			writeError(writer, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid or missing API key.", nil)
			return
		}
		next.ServeHTTP(writer, request)
	}
}

func (api *API) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"}, maximumResponseBytes)
}

func (api *API) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
	defer cancel()
	for _, check := range api.readiness {
		if err := check(ctx); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "server_error", "not_ready", "The service is not ready.", nil)
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"}, maximumResponseBytes)
}

func writeError(writer http.ResponseWriter, status int, typeName, code, message string, param any) {
	writeJSON(writer, status, errorResponse{Error: openAIError{Message: message, Type: typeName, Code: code, Param: param}}, maximumResponseBytes)
}

func writeJSON(writer http.ResponseWriter, status int, value any, maximum int) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		writeInternalEncodingError(writer)
		return false
	}
	if len(encoded) > maximum {
		writeError(writer, http.StatusBadGateway, "server_error", "response_too_large", "The completed response exceeded the configured size limit.", nil)
		return false
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
	return true
}

func writeInternalEncodingError(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprint(writer, `{"error":{"message":"The response could not be encoded.","type":"server_error","param":null,"code":"response_encoding_error"}}`)
}
