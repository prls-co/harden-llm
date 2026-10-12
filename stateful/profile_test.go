package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-423

import (
	"bytes"
	"encoding/json"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixtureCatalog(t *testing.T) Catalog {
	t.Helper()
	data, err := os.ReadFile("../test/fixtures/reference/presets.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}
func fixtureSnapshot(t *testing.T) SaveProfileInput {
	p := fixtureCatalog(t)["CPA Responses"]
	return SaveProfileInput{Name: p.Name, PresetID: "CPA Responses", Definition: p.Definition, Inputs: p.Inputs, Binding: p.Binding}
}
func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestProfileContract(t *testing.T) {
	t.Parallel()
	t.Run("template selection copies full draft without execution", func(t *testing.T) {
		catalog := fixtureCatalog(t)
		p := catalog["CPA Responses"]
		draft := presetDraft("CPA Responses", p)
		if !sameJSON(draft.Definition, p.Definition) || !sameJSON(draft.Inputs, p.Inputs) || draft.PresetID != "CPA Responses" {
			t.Fatal("template selection changed complete setup")
		}
		draft.Definition[0] = 'X'
		draft.Inputs[0] = 'X'
		if !json.Valid(p.Definition) || !json.Valid(p.Inputs) {
			t.Fatal("template buffers remain caller-owned")
		}
	})
	t.Run("explicit false zero null and empty are preserved", func(t *testing.T) {
		input := fixtureSnapshot(t)
		input.Inputs = json.RawMessage(`{"systemPrompt":"","providerOptions":{"zero":0,"disabled":false,"nullable":null},"webSearch":false,"timeoutMs":0}`)
		normalized, err := validateSnapshot(input)
		if err != nil || !sameJSON(input.Inputs, normalized.Inputs) {
			t.Fatalf("explicit values changed: %s %v", normalized.Inputs, err)
		}
		normalized.Inputs[0] = 'X'
		if !json.Valid(input.Inputs) {
			t.Fatal("validator retained input buffers")
		}
	})
	t.Run("explicit saves create UUID date suffix snapshots", func(t *testing.T) {
		now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
		input := fixtureSnapshot(t)
		one, err := snapshotRecord(strings.Repeat("a", 64), input, now, "01234567-0000-4000-8000-000000000001")
		if err != nil {
			t.Fatal(err)
		}
		two, err := snapshotRecord(strings.Repeat("a", 64), input, now, "abcdef00-0000-4000-8000-000000000002")
		if err != nil || one.ID == two.ID || !one.CreatedAt.Equal(now) || one.Name != "CPA Responses · 2026-10-10 12:00Z · 012345" || two.Name != "CPA Responses · 2026-10-10 12:00Z · abcdef" {
			t.Fatalf("save identity/time/name: %+v %+v %v", one, two, err)
		}
	})
	t.Run("overlong generated name fails rather than truncates", func(t *testing.T) {
		input := fixtureSnapshot(t)
		input.Name = strings.Repeat("x", 1499)
		if _, err := snapshotRecord(strings.Repeat("a", 64), input, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), "01234567-0000-4000-8000-000000000001"); err == nil {
			t.Fatal("generated name overflow was accepted")
		}
	})
	t.Run("saved setup survives template edit and deletion", func(t *testing.T) {
		catalog := fixtureCatalog(t)
		p := catalog["CPA Responses"]
		input := SaveProfileInput{Name: p.Name, PresetID: "CPA Responses", Definition: p.Definition, Inputs: p.Inputs, Binding: p.Binding}
		saved, err := snapshotRecord(strings.Repeat("a", 64), input, time.Now().UTC(), "01234567-0000-4000-8000-000000000001")
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(saved)
		p.Definition[0] = 'X'
		p.Inputs[0] = 'X'
		delete(catalog, "CPA Responses")
		after, _ := json.Marshal(saved)
		if !bytes.Equal(before, after) {
			t.Fatal("saved snapshot inherits mutable template")
		}
	})
	t.Run("hidden template preference affects only selected scope", func(t *testing.T) {
		catalog := fixtureCatalog(t)
		a := presetViews(catalog, map[string]bool{"CPA Responses": true})
		b := presetViews(catalog, nil)
		if len(a) != 1 || a[0].ID != "Repair Provider" || len(b) != 2 {
			t.Fatalf("hidden template leaked: A=%v B=%v", a, b)
		}
	})
	t.Run("five profile recovery leaves resolve fixed connections in scope", func(t *testing.T) {
		raw := json.RawMessage(`{"maxAttempts":6,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0},"jsonRepair":{"initial":{"source":"profile","profileId":"repair"},"escalation":{"source":"profile","profileId":"repair"}},"rerun":{"target":{"source":"profile","profileId":"repair"},"jsonRepair":{"initial":{"source":"profile","profileId":"repair"},"escalation":{"source":"profile","profileId":"repair"}}}}`)
		generation := ResolvedProfile{}
		_ = generation
		generation.Connection = hardenllm.Connection{ID: "generation", Provider: "cpa", Protocol: "responses", BaseURL: "https://cpa.example/v1", APIKey: "deployment-sentinel"}
		generation.Definition = fixtureCatalog(t)["CPA Responses"].Definition
		repair := ResolvedProfile{Connection: hardenllm.Connection{ID: "unused", Provider: "openai", Protocol: "responses", BaseURL: "https://repair.example/v1", APIKey: "repair-secret-sentinel"}}
		repair.Definition = fixtureCatalog(t)["Repair Provider"].Definition
		calls := 0
		policy, connections, err := resolveRecovery(raw, generation, func(id string) (ResolvedProfile, error) {
			calls++
			if id != "repair" {
				return ResolvedProfile{}, errors.New("profile_not_found")
			}
			return repair, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		targets := []hardenllm.RecoveryTarget{policy.JSONRepair.Initial, *policy.JSONRepair.Escalation, policy.Rerun.Target, policy.Rerun.JSONRepair.Initial, *policy.Rerun.JSONRepair.Escalation}
		want := []string{"repair-initial", "repair-escalation", "rerun", "rerun-repair-initial", "rerun-repair-escalation"}
		if calls != 5 || len(connections) != 6 {
			t.Fatalf("resolved snapshot: lookups=%d connections=%d", calls, len(connections))
		}
		for i, target := range targets {
			if target.Source != "model" || target.ConnectionID != want[i] || target.ModelID != "gpt-5.6-luna" || connections[want[i]].APIKey != "repair-secret-sentinel" {
				t.Errorf("leaf %d: %+v", i, target)
			}
		}
		if _, _, err := resolveRecovery(raw, generation, func(string) (ResolvedProfile, error) { return ResolvedProfile{}, errors.New("profile_not_found") }); err == nil {
			t.Fatal("foreign or absent profile silently substituted")
		}
	})
	t.Run("credential sources require exactly their own binding", func(t *testing.T) {
		for _, binding := range []ProfileCredentialBinding{{}, {Source: "deployment"}, {Source: "entered"}, {Source: "deployment", DeploymentUpstreamID: "cpa", EnteredCredentialID: "entered"}, {Source: "entered", DeploymentUpstreamID: "cpa", EnteredCredentialID: "entered"}} {
			input := fixtureSnapshot(t)
			input.Binding = binding
			if _, err := validateSnapshot(input); err == nil {
				t.Errorf("invalid binding accepted: %+v", binding)
			}
		}
		for _, binding := range []ProfileCredentialBinding{{Source: "deployment", DeploymentUpstreamID: "cpa"}, {Source: "entered", EnteredCredentialID: "fixture"}} {
			input := fixtureSnapshot(t)
			input.Binding = binding
			if _, err := validateSnapshot(input); err != nil {
				t.Errorf("valid binding: %v", err)
			}
		}
	})
	t.Run("picker omits full inputs and detail contains them without secrets", func(t *testing.T) {
		input := fixtureSnapshot(t)
		input.Inputs = json.RawMessage(`{"userPrompt":"prompt-fixture","systemPrompt":"system-fixture","schema":{"type":"object"}}`)
		record, err := snapshotRecord(strings.Repeat("a", 64), input, time.Now().UTC(), "01234567-0000-4000-8000-000000000001")
		if err != nil {
			t.Fatal(err)
		}
		picker, _ := json.Marshal(profileView(record, CredentialStatus{Configured: true}))
		detail, _ := json.Marshal(recordDraft(record, CredentialStatus{Configured: true}))
		for _, s := range []string{"prompt-fixture", "system-fixture", "\"inputs\"", "apiKeyHash", "deployment-sentinel"} {
			if bytes.Contains(picker, []byte(s)) {
				t.Errorf("picker includes %s", s)
			}
		}
		if !bytes.Contains(detail, []byte("prompt-fixture")) || bytes.Contains(detail, []byte("deployment-sentinel")) || bytes.Contains(detail, []byte("apiKeyHash")) {
			t.Fatalf("detail lost setup or leaked secrets: %s", detail)
		}
	})
	t.Run("caller cannot assign snapshot identity revision or creation time", func(t *testing.T) {
		input := fixtureSnapshot(t)
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseSaveProfile(data); err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"id", "revision", "createdAt", "apiKeyHash", "userId", "ownerId", "sessionReference"} {
			fields[name] = "caller-value"
			data, _ := json.Marshal(fields)
			if _, err := parseSaveProfile(data); err == nil {
				t.Errorf("caller field %s accepted", name)
			}
			delete(fields, name)
		}
	})
}
