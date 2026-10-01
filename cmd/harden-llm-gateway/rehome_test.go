package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022 TEST-053

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadIdentityMapping(t *testing.T) {
	t.Parallel()

	valid := `{"owners":[{"localOwnerId":"operator-local","accountId":"11111111-1111-4111-8111-111111111111"},{"localOwnerId":"guest","accountId":"22222222-2222-4222-8222-222222222222"}]}`
	path := writeIdentityMappingFixture(t, valid)
	mapping, err := readIdentityMapping(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"operator-local": "11111111-1111-4111-8111-111111111111",
		"guest":          "22222222-2222-4222-8222-222222222222",
	}
	if !reflect.DeepEqual(mapping, want) {
		t.Fatalf("mapping = %#v, want %#v", mapping, want)
	}
}

func TestReadIdentityMappingRejectsAmbiguousOrInvalidDocuments(t *testing.T) {
	t.Parallel()

	validOwner := `{"localOwnerId":"operator-local","accountId":"11111111-1111-4111-8111-111111111111"}`
	cases := map[string]string{
		"empty owners":        `{"owners":[]}`,
		"duplicate source":    `{"owners":[` + validOwner + `,` + validOwner + `]}`,
		"consolidated target": `{"owners":[{"localOwnerId":"operator-local","accountId":"11111111-1111-4111-8111-111111111111"},{"localOwnerId":"guest","accountId":"11111111-1111-4111-8111-111111111111"}]}`,
		"invalid account":     `{"owners":[{"localOwnerId":"operator-local","accountId":"not-a-uuid"}]}`,
		"unknown field":       `{"owners":[` + validOwner + `],"fallback":true}`,
		"second document":     `{"owners":[` + validOwner + `]} {}`,
		"whitespace owner":    `{"owners":[{"localOwnerId":" operator-local","accountId":"11111111-1111-4111-8111-111111111111"}]}`,
	}
	for name, contents := range cases {
		name, contents := name, contents
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := readIdentityMapping(writeIdentityMappingFixture(t, contents)); err == nil {
				t.Fatal("invalid identity mapping was accepted")
			}
		})
	}
}

func TestIdentityMappingStateRequiresExactOwnerSet(t *testing.T) {
	t.Parallel()

	mapping := map[string]string{"operator-local": "11111111-1111-4111-8111-111111111111", "guest": "22222222-2222-4222-8222-222222222222"}
	if !identityMappingMatches([]string{"guest", "operator-local"}, mapping, false) {
		t.Fatal("exact legacy owner set was not recognized")
	}
	if !identityMappingMatches([]string{"22222222-2222-4222-8222-222222222222", "11111111-1111-4111-8111-111111111111"}, mapping, true) {
		t.Fatal("exact already-migrated account set was not recognized")
	}
	for _, actual := range [][]string{{"operator-local"}, {"guest", "unexpected"}, {"guest", "operator-local", "operator-local"}} {
		if identityMappingMatches(actual, mapping, false) {
			t.Errorf("inexact legacy owner set %q was accepted", actual)
		}
	}
}

func writeIdentityMappingFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity-map.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
