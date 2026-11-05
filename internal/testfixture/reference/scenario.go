// Package reference is a test-only, deterministic native provider boundary.
// It does not execute inference, validate model output or implement recovery:
// the actual gateway/stateful/core/provider code owns those behaviors.
package reference

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/internal/testfixture/native"
)

const maxRequestBytes = 256 << 10

type document struct {
	SchemaVersion string      `json:"schemaVersion"`
	Operations    []operation `json:"operations"`
}

type operation struct {
	OperationPattern string `json:"operationPattern"`
	Steps            []step `json:"steps"`
	pattern          *regexp.Regexp
}

type step struct {
	Match         nativeMatch     `json:"match"`
	Kind          string          `json:"kind"`
	Text          json.RawMessage `json:"text,omitempty"`
	Status        json.RawMessage `json:"status,omitempty"`
	SearchSources json.RawMessage `json:"searchSources,omitempty"`
	DelayMs       json.RawMessage `json:"delayMs,omitempty"` // Optional test latency; never a production retry/timeout.
	text          string
	status        int
	sources       []runtime.SearchSource
	delay         time.Duration
}

type nativeMatch struct {
	Path     string `json:"path"`
	ModelID  string `json:"modelId"`
	Contains string `json:"contains"`
}

// Stats contains only logical IDs and actual boundary counts. A Run rejected
// by the owner or served from cache can consume zero provider steps.
type Stats struct {
	SchemaVersion          string                    `json:"schemaVersion"`
	RunPostCount           int                       `json:"runPostCount"`
	ProviderRequestCount   int                       `json:"providerRequestCount"`
	UnmatchedRunCount      int                       `json:"unmatchedRunCount"`
	UnmatchedProviderCount int                       `json:"unmatchedProviderCount"`
	Operations             map[string]OperationStats `json:"operations"`
}

type OperationStats struct {
	RunPostCount          int `json:"runPostCount"`
	ProviderDispatchCount int `json:"providerDispatchCount"`
	ConsumedSteps         int `json:"consumedSteps"`
}

type run struct {
	operation int
	active    bool
	stats     OperationStats
}

type Scenario struct {
	mu         sync.Mutex
	operations []operation
	runs       map[string]*run
	stats      Stats
}

// Decode freezes the complete input before any request is served. Parser errors
// intentionally omit raw JSON, patterns, prompts and caller-supplied values.
func Decode(input io.Reader) (*Scenario, error) {
	var doc document
	if strictJSON(input, &doc) != nil || doc.SchemaVersion != "prls.reference-gateway-fixture.scenario.v1" || len(doc.Operations) == 0 {
		return nil, errors.New("fixture: invalid scenario document")
	}
	for index := range doc.Operations {
		op := &doc.Operations[index]
		pattern, err := regexp.Compile(op.OperationPattern)
		if err != nil || !strings.HasPrefix(op.OperationPattern, "^") || !strings.HasSuffix(op.OperationPattern, "$") || len(op.Steps) == 0 {
			return nil, errors.New("fixture: invalid operation rule")
		}
		op.pattern = pattern
		for index := range op.Steps {
			item := &op.Steps[index]
			path, err := url.Parse(item.Match.Path)
			if err != nil || path.IsAbs() || path.Host != "" || path.RawQuery != "" || path.Fragment != "" || path.Path != item.Match.Path || !strings.HasPrefix(path.Path, "/") || strings.HasPrefix(path.Path, "//") || strings.TrimSpace(item.Match.ModelID) == "" || strings.TrimSpace(item.Match.Contains) == "" {
				return nil, errors.New("fixture: invalid native request match")
			}
			if len(item.SearchSources) != 0 {
				if item.Kind != "output" || item.Match.Path != "/v1/responses" || strictJSON(bytes.NewReader(item.SearchSources), &item.sources) != nil || len(item.sources) == 0 || len(item.sources) > 50 {
					return nil, errors.New("fixture: invalid native search sources")
				}
				seen := make(map[string]bool, len(item.sources))
				for _, source := range item.sources {
					if !runtime.ValidSearchURL(source.URL) || strings.TrimSpace(source.Title) == "" || seen[source.URL] {
						return nil, errors.New("fixture: invalid native search source")
					}
					seen[source.URL] = true
				}
			}
			if len(item.DelayMs) != 0 {
				var milliseconds *int
				if item.Kind != "output" || item.Match.Path != "/v1/responses" || json.Unmarshal(item.DelayMs, &milliseconds) != nil || milliseconds == nil || *milliseconds < 1 || *milliseconds > 60000 {
					return nil, errors.New("fixture: output delay requires an integer from 1 to 60000 milliseconds")
				}
				item.delay = time.Duration(*milliseconds) * time.Millisecond
			}
			switch item.Kind {
			case "output":
				var text *string
				if json.Unmarshal(item.Text, &text) != nil || text == nil || len(item.Status) != 0 {
					return nil, errors.New("fixture: output requires only explicit text")
				}
				item.text = *text
			case "http_error":
				if len(item.Text) != 0 || json.Unmarshal(item.Status, &item.status) != nil || item.status < 400 || item.status > 599 {
					return nil, errors.New("fixture: invalid HTTP fault")
				}
			case "disconnect", "wait_for_cancel":
				if len(item.Text) != 0 || len(item.Status) != 0 {
					return nil, errors.New("fixture: invalid transport fault")
				}
			default:
				return nil, errors.New("fixture: unsupported step kind")
			}
		}
	}
	return &Scenario{operations: doc.Operations, runs: make(map[string]*run), stats: Stats{SchemaVersion: "prls.reference-gateway-fixture.stats.v1"}}, nil
}

func strictJSON(input io.Reader, value any) error {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("fixture: expected one JSON document")
	}
	return nil
}

// BeginRun records an actual authorized Run POST, not an inferred model attempt.
// Ambiguous rule matches and overlapping identical IDs fail without dispatch.
func (scenario *Scenario) BeginRun(id string) (func(), error) {
	scenario.mu.Lock()
	defer scenario.mu.Unlock()
	scenario.stats.RunPostCount++
	selected, matches := 0, 0
	for index, op := range scenario.operations {
		if op.pattern.MatchString(id) {
			selected, matches = index, matches+1
		}
	}
	previous := scenario.runs[id]
	if id == "" || matches != 1 || previous != nil && previous.active {
		scenario.stats.UnmatchedRunCount++
		return nil, errors.New("fixture: unknown, ambiguous or overlapping logical operation")
	}
	if previous == nil {
		previous = &run{operation: selected}
		scenario.runs[id] = previous
	}
	previous.active = true
	previous.stats.RunPostCount++
	return sync.OnceFunc(func() {
		scenario.mu.Lock()
		defer scenario.mu.Unlock()
		previous.active = false
	}), nil
}

func (scenario *Scenario) Stats() Stats {
	scenario.mu.Lock()
	defer scenario.mu.Unlock()
	stats := scenario.stats
	stats.Operations = make(map[string]OperationStats, len(scenario.runs))
	for id, item := range scenario.runs {
		stats.Operations[id] = item.stats
	}
	return stats
}

// Gateway adds only fixture accounting/stats to the real owner handler. It
// leaves authentication and the full Run input validation at their owner.
func (scenario *Scenario) Gateway(key string, owner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := auth.MatchBearer(r.Header.Values("Authorization"), key)
		if r.URL.Path == "/fixture/stats" {
			if !authorized {
				http.Error(w, "fixture: unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(scenario.Stats())
			return
		}
		if authorized && r.Method == http.MethodPost && r.URL.Path == "/api/v1/run" {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
			_ = r.Body.Close()
			if err != nil || len(body) > maxRequestBytes {
				http.Error(w, "fixture: invalid Run body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			// The owner decodes the rest; this wrapper needs only correlation.
			var metadata struct {
				Origin struct {
					OperationID string `json:"operationId"`
				} `json:"origin"`
			}
			if json.Unmarshal(body, &metadata) != nil {
				http.Error(w, "fixture: invalid Run metadata", http.StatusBadRequest)
				return
			}
			finish, err := scenario.BeginRun(metadata.Origin.OperationID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			defer finish()
		}
		owner.ServeHTTP(w, r)
	})
}

func (scenario *Scenario) Provider() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		encoded, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		valid := err == nil && len(encoded) <= maxRequestBytes && r.Method == http.MethodPost && r.URL.RawQuery == "" && strictJSON(bytes.NewReader(encoded), &body) == nil && body != nil
		item, ok := scenario.next(r.URL.Path, body, valid)
		if !ok {
			http.Error(w, "fixture: no unique active native step", http.StatusConflict)
			return
		}
		switch item.Kind {
		case "output":
			if item.delay != 0 {
				if r.Context().Err() != nil {
					return
				}
				// This is accepted native HTTP followed by quiet model output,
				// not a connection that violates the owner's header timeout.
				native.WriteResponseHeaders(w, r, body)
				if http.NewResponseController(w).Flush() != nil {
					return
				}
				timer := time.NewTimer(item.delay)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
			}
			native.WriteResponse(w, r, body, item.text, item.sources...)
		case "http_error":
			http.Error(w, "fixture: scenario HTTP fault", item.status)
		case "disconnect":
			connection, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = connection.Close()
			} else {
				http.Error(w, "fixture: transport cannot disconnect", http.StatusInternalServerError)
			}
		case "wait_for_cancel":
			<-r.Context().Done()
		}
	})
}

func (scenario *Scenario) next(path string, body map[string]any, valid bool) (step, bool) {
	scenario.mu.Lock()
	defer scenario.mu.Unlock()
	scenario.stats.ProviderRequestCount++
	var selected *run
	var item step
	matches := 0
	for _, candidate := range scenario.runs {
		steps := scenario.operations[candidate.operation].Steps
		if !valid || !candidate.active || candidate.stats.ConsumedSteps >= len(steps) {
			continue
		}
		next := steps[candidate.stats.ConsumedSteps]
		model, _ := body["model"].(string)
		if model == "" && strings.HasPrefix(path, "/v1beta/models/") && strings.HasSuffix(path, ":generateContent") {
			model = strings.TrimSuffix(strings.TrimPrefix(path, "/v1beta/models/"), ":generateContent")
		}
		if path == next.Match.Path && model == next.Match.ModelID && containsValue(body, next.Match.Contains) && (len(next.sources) == 0 || hasNativeSearchTool(body)) {
			selected, item, matches = candidate, next, matches+1
		}
	}
	if matches != 1 {
		scenario.stats.UnmatchedProviderCount++
		return step{}, false
	}
	selected.stats.ProviderDispatchCount++
	selected.stats.ConsumedSteps++
	return item, true
}

func hasNativeSearchTool(body map[string]any) bool {
	tools, _ := body["tools"].([]any)
	for _, tool := range tools {
		object, _ := tool.(map[string]any)
		if object["type"] == "web_search" {
			return true
		}
	}
	return false
}

// Match decoded string values across native protocols without copying their
// request codecs. JSON property names cannot satisfy a prompt marker.
func containsValue(value any, marker string) bool {
	switch value := value.(type) {
	case string:
		return strings.Contains(value, marker)
	case []any:
		for _, item := range value {
			if containsValue(item, marker) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if containsValue(item, marker) {
				return true
			}
		}
	}
	return false
}
