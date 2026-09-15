package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestSyncProfilesRejectsInvalidInputWithoutLeakingSecrets(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"secret-fixture", `{"unknown":"secret-fixture"}`, `{} {"secret-fixture":true}`} {
		err := runSyncProfiles(context.Background(), []string{"--email", "guest@example.test"}, strings.NewReader(input), io.Discard, func(string) string { return "" })
		if err == nil || strings.Contains(err.Error(), "secret-fixture") {
			t.Fatal("invalid input accepted or leaked")
		}
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-207
func TestSyncProfilesRejectsOldCatalogBeforeEnvironmentAccess(t *testing.T) {
	catalog, err := profiles.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for id, profile := range catalog {
		profile.SchemaVersion = 1
		catalog[id] = profile
	}
	raw, err := json.Marshal(gateway.SharedProfiles{Profiles: catalog})
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	err = runSyncProfiles(context.Background(), []string{"--email", "owner@example.test"}, strings.NewReader(string(raw)), io.Discard, func(string) string { reads++; return "" })
	if err == nil || !strings.Contains(err.Error(), "schemaVersion 2") || reads != 0 {
		t.Fatalf("old configuration: %v, environment reads=%d", err, reads)
	}
}
