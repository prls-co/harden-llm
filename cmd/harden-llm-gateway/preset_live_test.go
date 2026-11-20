//go:build live

package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-455

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

// Explicit live-provider selection only; no SQL, history, cache or browser.
// Each configured candidate executes exactly once per mode. Failures are
// retained and never retried by the canary or hardening policy.
func TestLivePresetMatrix(t *testing.T) {
	if os.Getenv("HARDEN_LLM_LIVE_PRESETS") != "1" {
		t.Skip("explicit live preset authorization not supplied")
	}
	config, err := loadServerConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: config.connections, DefaultConnection: config.defaultConnection, WebSearch: stateful.WebSearchOptions{JinaAPIKey: config.jinaAPIKey}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, Presets: catalog})
	if err != nil {
		t.Fatal(err)
	}
	reportPath := os.Getenv("HARDEN_LLM_LIVE_PRESET_REPORT")
	if reportPath == "" {
		t.Fatal("private report path required")
	}
	search := os.Getenv("HARDEN_LLM_LIVE_PRESET_SEARCH")
	if search != "" && search != "1" {
		t.Fatal("HARDEN_LLM_LIVE_PRESET_SEARCH must be empty or 1")
	}
	modes := []hardenllm.CallType{hardenllm.CallTypeText, hardenllm.CallTypeStructured}
	switch os.Getenv("HARDEN_LLM_LIVE_PRESET_MODE") {
	case "":
	case "text":
		modes = []hardenllm.CallType{hardenllm.CallTypeText}
	case "structured":
		modes = []hardenllm.CallType{hardenllm.CallTypeStructured}
	default:
		t.Fatal("HARDEN_LLM_LIVE_PRESET_MODE must be empty, text or structured")
	}
	revision, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		t.Fatalf("could not identify source revision before dispatch: %v", err)
	}
	status, err := exec.CommandContext(t.Context(), "git", "status", "--porcelain=v1").Output()
	if err != nil {
		t.Fatalf("could not identify checkout state before dispatch: %v", err)
	}
	digest := sha256.Sum256([]byte(config.apiKeys[0]))
	scope := hex.EncodeToString(digest[:])
	available := map[string]bool{}
	for _, c := range config.connections {
		available[c.ID] = c.APIKey != ""
	}
	rows := []map[string]any{}
	inputs := []stateful.RunInput{}
	write := func() {
		if err := writeLivePresetReport(reportPath, rows); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		preset := catalog[id]
		for _, mode := range modes {
			input, profile, route, err := livePresetInput(id, preset, mode, search == "1")
			if err != nil {
				t.Fatal(err)
			}
			row := newLivePresetRow(preset, profile, input, route)
			row["sourceRevision"] = strings.TrimSpace(string(revision))
			row["sourceDirty"] = len(status) != 0
			if !available[preset.Binding.DeploymentUpstreamID] {
				row["status"], row["missing"] = "not_configured", "provider_credential"
			} else if route == "jina" && config.jinaAPIKey == "" {
				row["status"], row["missing"] = "not_configured", "jina_credential"
			}
			rows, inputs = append(rows, row), append(inputs, input)
		}
	}
	if err := createLivePresetReport(reportPath); err != nil {
		t.Fatal("new private report path required; existing evidence is never overwritten")
	}
	write()
	for index, input := range inputs {
		row := rows[index]
		if row["status"] != "planned" {
			continue
		}
		var reportErr error
		t.Run(input.ProfileID+"/"+string(input.CallType), func(t *testing.T) {
			// Persist intent only after Go selects this subtest. An interrupted
			// started row is unconfirmed and must never be replayed automatically.
			row["status"], row["startedAt"] = "started", time.Now().UTC().Format(time.RFC3339Nano)
			if reportErr = writeLivePresetReport(reportPath, rows); reportErr != nil {
				t.Error("could not preserve dispatch intent; no call made")
				return
			}
			output, _, callErr := service.Run(context.Background(), scope, input)
			if !recordLivePresetOutcome(row, input, output, callErr, time.Now().UTC()) {
				t.Errorf("preset qualification failed: qualification=%v status=%v capture=%v errorCode=%v diagnosticCode=%v providerInvoked=%v", row["qualificationStatus"], row["status"], row["diagnosticCaptureStatus"], row["errorCode"], row["diagnosticCode"], output.ProviderInvoked)
			}
		})
		if reportErr != nil {
			t.Fatal(reportErr)
		}
		write()
	}
}
