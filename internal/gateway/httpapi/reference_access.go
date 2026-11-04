package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"io"
	"net/http"
	"strings"
)

type accessScope struct {
	hash   string
	client *hardenllm.Client
}
type accessScopeKey struct{}

func scopeFromContext(ctx context.Context) accessScope {
	scope, _ := ctx.Value(accessScopeKey{}).(accessScope)
	return scope
}
func scopeSelector(field string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(field))
	switch normalized {
	case "apikeyhash", "owner", "ownerid", "userid", "sessionreference", "sessionref":
		return true
	}
	return false
}
func rejectScopeSelector(w http.ResponseWriter, r *http.Request) bool {
	for name := range r.URL.Query() {
		if scopeSelector(name) {
			writeProtocolError(w, r, 400, "invalid_request_error", "scope_selector_rejected", "Scope is derived from the authenticated API key.", name)
			return true
		}
	}
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	limit := maximumRequestBytes
	bundle := r.Method == http.MethodPut && r.URL.Path == "/api/v1/profiles/bundle"
	if bundle {
		limit = 2 << 20
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
		limit = maximumJobRequestBytes
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)))
	_ = r.Body.Close()
	if err != nil {
		writeProtocolError(w, r, 413, "invalid_request_error", "request_too_large", "Request exceeds its operation size limit.", nil)
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) == nil {
		for name := range fields {
			if scopeSelector(name) && !(bundle && name == "apiKeyHash") {
				writeProtocolError(w, r, 400, "invalid_request_error", "scope_selector_rejected", "Scope is derived from the authenticated API key.", name)
				return true
			}
		}
	}
	return false
}
func (api *API) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		matched := ""
		for _, key := range api.keys {
			if auth.MatchBearer(r.Header.Values("Authorization"), key) {
				matched = key
			}
		}
		if matched == "" {
			writeProtocolError(w, r, 401, "authentication_error", "invalid_api_key", "Invalid or missing API key.", nil)
			return
		}
		if rejectScopeSelector(w, r) {
			return
		}
		digest := sha256.Sum256([]byte(matched))
		hash := hex.EncodeToString(digest[:])
		client, err := api.clientForKey(hash)
		if err != nil {
			writeProtocolError(w, r, 503, "server_error", "service_unavailable", "The inference composition is unavailable.", nil)
			return
		}
		ctx := context.WithValue(r.Context(), accessScopeKey{}, accessScope{hash: hash, client: client})
		next(w, r.WithContext(ctx))
	}
}
