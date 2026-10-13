package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-436

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func referenceFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(readFile(t, filepath.Join(repositoryRoot(t), "test/fixtures/reference", name)), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReferenceHistoryContract(t *testing.T) {
	t.Parallel()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Join(repositoryRoot(t), "api/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	validate := func(name string, value any) {
		t.Helper()
		schema, ok := document.Components.Schemas[name]
		if !ok || schema.Value == nil {
			t.Errorf("canonical schema %s is not defined", name)
			return
		}
		if err := schema.Value.VisitJSON(value); err != nil {
			t.Errorf("%s fixture violates the canonical schema: %v", name, err)
		}
	}
	hashPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	outcomes := referenceFixture(t, "history-outcomes.json")
	statuses := map[string]bool{}
	for _, raw := range outcomes["outcomes"].([]any) {
		value := raw.(map[string]any)
		validate("RecordingOutcome", value)
		statuses[value["status"].(string)] = true
	}
	if len(statuses) != 5 {
		t.Error("recording fixtures must cover exactly five statuses")
	}
	for _, status := range []string{"disabled", "configuration_error", "saved", "not_saved", "unconfirmed"} {
		if !statuses[status] {
			t.Errorf("recording status %s is not covered", status)
		}
	}
	for _, raw := range outcomes["commitCases"].([]any) {
		value := raw.(map[string]any)
		if value["commit"] == "unknown" && value["readback"] != "found" && value["status"] != "unconfirmed" {
			t.Error("absent/failed readback cannot prove an uncertain COMMIT was rejected")
		}
		if value["inserts"] != float64(1) || value["reads"].(float64) > 1 {
			t.Error("recording must have one insert and at most one readback")
		}
	}
	pages := referenceFixture(t, "history-pages.json")
	workspace := referenceFixture(t, "workspace.json")
	for _, event := range workspace["stream"].([]any) {
		validate("ReferenceRunProgressEvent", event)
		value := event.(map[string]any)
		if value["type"] == "run.completed" {
			validate("RecordedRunOutput", value["data"].(map[string]any)["result"])
		}
		if value["type"] == "history.recorded" {
			validate("RecordingOutcome", value["data"])
		}
	}
	for schema, key := range map[string]string{"ProfilesEnvelope": "profiles", "ProfileEnvelope": "savedProfile", "HistoryPage": "history", "HistoryRecord": "record", "RunEnvelope": "run", "Statistics": "statistics", "ReferenceStatus": "status"} {
		validate(schema, workspace[key])
	}
	for _, draft := range workspace["details"].(map[string]any) {
		validate("ProfileDraft", draft)
	}
	validate("HistoryPage", pages["page"])
	for _, raw := range pages["rows"].([]any) {
		row := raw.(map[string]any)
		if hash, ok := row["apiKeyHash"].(string); !ok || !hashPattern.MatchString(hash) {
			t.Error("every stored history row needs its canonical server-derived hash")
		}
		record := row["record"].(map[string]any)
		validate("HistoryRecord", record)
		if _, exists := record["apiKeyHash"]; exists {
			t.Error("public history DTOs must not expose the storage scope")
		}
		if _, exists := record["result"].(map[string]any)["history"]; exists {
			t.Error("stored result must not contain its own recording outcome")
		}
	}
	artifacts := referenceFixture(t, "artifact-manifest.json")
	hash := artifacts["apiKeyHash"].(string)
	if !hashPattern.MatchString(hash) {
		t.Error("artifact fixture scope is invalid")
	}
	for _, raw := range artifacts["artifacts"].([]any) {
		artifact := raw.(map[string]any)
		metadata := artifact["metadata"].(map[string]any)
		validate("ArtifactMetadata", metadata)
		want := "artifacts/" + hash + "/" + artifacts["runId"].(string) + "/" + metadata["artifactId"].(string)
		if artifact["objectKey"] != want {
			t.Error("artifact storage path must use its own key/run prefix")
		}
		for _, field := range []string{"objectKey", "url", "apiKeyHash", "ownerId", "userId"} {
			if _, exists := artifact["request"].(map[string]any)[field]; exists {
				t.Errorf("artifact caller supplies forbidden selector %s", field)
			}
		}
	}
	var rejectSecrets func(any)
	rejectSecrets = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				switch strings.ToLower(key) {
				case "apikey", "authorization", "password", "secret", "sessionreference":
					t.Errorf("history/artifact fixture includes forbidden secret field %s", key)
				}
				rejectSecrets(child)
			}
		case []any:
			for _, child := range typed {
				rejectSecrets(child)
			}
		}
	}
	for _, fixture := range []map[string]any{outcomes, pages, artifacts} {
		rejectSecrets(fixture)
	}
}
