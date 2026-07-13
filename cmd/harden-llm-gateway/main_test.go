package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBootstrapCommandInput(t *testing.T) {
	password, err := loadPassword(strings.NewReader("correct horse battery staple\n"), "-")
	if err != nil || password != "correct horse battery staple" {
		t.Fatalf("password input = %q, %v", password, err)
	}
	if _, err := loadPassword(strings.NewReader(strings.Repeat("x", maximumPasswordBytes+1)), "-"); err == nil {
		t.Fatal("oversized password input was accepted")
	}
	var output bytes.Buffer
	err = run(context.Background(), []string{"bootstrap-user", "--owner-id", "owner-a", "--email", "a@example.test"}, strings.NewReader("do-not-echo-this-password\n"), &output, &output, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), databaseURLEnvironment) || strings.Contains(err.Error(), "do-not-echo") || output.Len() != 0 {
		t.Fatalf("missing database configuration = %v, output=%q", err, output.String())
	}
	if err := run(context.Background(), []string{"unknown"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil {
		t.Fatal("unknown command was accepted")
	}
}
