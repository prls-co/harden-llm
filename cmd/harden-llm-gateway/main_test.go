package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022 TEST-061

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLocalPasswordBootstrapCommandIsRemoved(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), []string{"bootstrap-user"}, strings.NewReader(""), &output, &output, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "unknown command") || output.Len() != 0 {
		t.Fatalf("retired password bootstrap command = %v, output=%q", err, output.String())
	}
	if err := run(context.Background(), []string{"unknown"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil {
		t.Fatal("unknown command was accepted")
	}
	output.Reset()
	if err := run(context.Background(), []string{"reconcile-history"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("retired history migration command = %v", err)
	}
	if err := run(context.Background(), []string{"audit-artifacts"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), databaseURLEnvironment) {
		t.Fatalf("missing artifact audit configuration = %v", err)
	}
	if err := run(context.Background(), []string{"audit-artifacts", "unexpected"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil {
		t.Fatal("artifact audit accepted arguments")
	}
}

func TestVersionCommand(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), []string{"version"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if output.String() != version+"\n" {
		t.Fatalf("version output = %q", output.String())
	}
	if err := run(context.Background(), []string{"version", "unexpected"}, strings.NewReader(""), &output, &output, func(string) string { return "" }); err == nil {
		t.Fatal("version command accepted arguments")
	}
}
