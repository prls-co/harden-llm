package cachekey

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-011

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestCacheIdentity(t *testing.T) {
	fixture := loadCacheFixture(t)
	operation := fixture.Operation.toOperation()
	digest, err := Hash(operation, fixture.CacheVersion)
	if err != nil {
		t.Fatal(err)
	}
	if digest != fixture.OperationHash {
		t.Fatalf("Hash() = %s, want source %s", digest, fixture.OperationHash)
	}
	stable, err := StableJSON(map[string]any{"operation": operation, "cacheVersion": fixture.CacheVersion})
	if err != nil {
		t.Fatal(err)
	}
	if string(stable) != fixture.StablePayload {
		t.Fatalf("stable payload mismatch:\n got: %s\nwant: %s", stable, fixture.StablePayload)
	}

	for name, mutate := range map[string]func(*Operation){
		"model":           func(value *Operation) { value.Model = "other-model" },
		"payload":         func(value *Operation) { value.Payload = map[string]any{"different": true} },
		"semantic header": func(value *Operation) { value.SemanticHeaders = map[string]any{"openai-beta": "other"} },
		"projection":      func(value *Operation) { value.ResponseProjection.Version = "v2" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := operation
			mutate(&changed)
			changedHash, err := Hash(changed, fixture.CacheVersion)
			if err != nil {
				t.Fatal(err)
			}
			if changedHash == digest {
				t.Fatal("semantic change did not alter cache identity")
			}
		})
	}

	for _, mode := range fixture.Modes {
		input := ""
		if mode.Input != nil {
			input = *mode.Input
		}
		resolved, err := ResolveMode(input)
		if err != nil || string(resolved) != mode.Output {
			t.Fatalf("ResolveMode(%q) = %q/%v, want %q", input, resolved, err, mode.Output)
		}
	}
	if _, err := ResolveMode("unknown"); err == nil {
		t.Fatal("unsupported cache mode accepted")
	}

	unsafe := operation
	unsafe.SemanticHeaders = map[string]any{"authorization": "fixture-secret"}
	if _, err := Hash(unsafe, fixture.CacheVersion); err == nil {
		t.Fatal("secret semantic header accepted")
	}
	unsafe = operation
	unsafe.Payload = map[string]any{"value": math.NaN()}
	if _, err := Hash(unsafe, fixture.CacheVersion); err == nil {
		t.Fatal("non-JSON-safe payload accepted")
	}
}

type cacheFixture struct {
	CacheVersion  string           `json:"cacheVersion"`
	OperationHash string           `json:"operationHash"`
	StablePayload string           `json:"stablePayload"`
	Operation     fixtureOperation `json:"operation"`
	Modes         []struct {
		Input  *string `json:"input"`
		Output string  `json:"output"`
	} `json:"modes"`
}

type fixtureOperation struct {
	SchemaVersion      string             `json:"schemaVersion"`
	Protocol           string             `json:"protocol"`
	Endpoint           Endpoint           `json:"endpoint"`
	Model              string             `json:"model"`
	Payload            any                `json:"payload"`
	SemanticHeaders    map[string]any     `json:"semanticHeaders"`
	ResponseProjection ResponseProjection `json:"responseProjection"`
}

func (fixture fixtureOperation) toOperation() Operation {
	return Operation{
		SchemaVersion:      fixture.SchemaVersion,
		Protocol:           fixture.Protocol,
		Endpoint:           fixture.Endpoint,
		Model:              fixture.Model,
		Payload:            fixture.Payload,
		SemanticHeaders:    fixture.SemanticHeaders,
		ResponseProjection: fixture.ResponseProjection,
	}
}

func loadCacheFixture(t *testing.T) cacheFixture {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "parity", "generated", "cache-identity.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture cacheFixture
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestStableJSONDeterministic(t *testing.T) {
	left, err := StableJSON(map[string]any{"b": 2, "a": []any{map[string]any{"z": true, "a": nil}}})
	if err != nil {
		t.Fatal(err)
	}
	right, _ := StableJSON(map[string]any{"a": []any{map[string]any{"a": nil, "z": true}}, "b": 2})
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("canonical JSON differs: %s != %s", left, right)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-217
func TestRecoveryBoundaryAccountingCache(t *testing.T) {
	t.Parallel()
	base := Operation{
		SchemaVersion: OperationSchemaVersion, Protocol: "openai.responses",
		Endpoint: Endpoint{Identity: "https://provider.example", Method: "POST", Path: "/responses"},
		Model:    "fixture", Payload: map[string]any{"input": "prompt"}, SemanticHeaders: map[string]any{},
		ResponseProjection: ResponseProjection{Provider: "openai", Kind: "text", Version: "v4"},
	}
	currentHash, err := Hash(base, DefaultVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, oldVersion := range []string{"v1", "v2"} {
		old := base
		old.ResponseProjection.Version = oldVersion
		oldHash, hashErr := Hash(old, DefaultVersion)
		if hashErr != nil || oldHash == currentHash {
			t.Fatalf("old projection %s was not separated: %s/%v", oldVersion, oldHash, hashErr)
		}
	}
	if DefaultVersion != "operation-v2" {
		t.Fatalf("outer cache namespace changed: %q", DefaultVersion)
	}
	structured := base
	structured.ResponseProjection.Kind = "structured-output"
	structuredHash, err := Hash(structured, DefaultVersion)
	if err != nil || structuredHash == currentHash {
		t.Fatalf("text and structured current projections collided: %s/%v", structuredHash, err)
	}
}
