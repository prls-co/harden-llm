//go:build live

package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-455

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sort"
	"testing"

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
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: config.connections, DefaultConnection: config.defaultConnection, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, Presets: catalog})
	if err != nil {
		t.Fatal(err)
	}
	reportPath := os.Getenv("HARDEN_LLM_LIVE_PRESET_REPORT")
	if reportPath == "" {
		t.Fatal("private report path required")
	}
	digest := sha256.Sum256([]byte(config.apiKeys[0]))
	scope := hex.EncodeToString(digest[:])
	available := map[string]bool{}
	for _, c := range config.connections {
		available[c.ID] = c.APIKey != ""
	}
	rows := []map[string]any{}
	write := func() {
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(reportPath, append(data, '\n'), 0600); err != nil {
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
		if !available[preset.Binding.DeploymentUpstreamID] {
			rows = append(rows, map[string]any{"id": id, "status": "not_configured"})
			write()
			continue
		}
		var inputs struct {
			ModelID   string          `json:"modelId"`
			Schema    json.RawMessage `json:"schema"`
			Reasoning string          `json:"reasoningEffort"`
			Options   map[string]any  `json:"providerOptions"`
		}
		if err = json.Unmarshal(preset.Inputs, &inputs); err != nil {
			t.Fatal(err)
		}
		modes := []hardenllm.CallType{hardenllm.CallTypeText, hardenllm.CallTypeStructured}
		if os.Getenv("HARDEN_LLM_LIVE_PRESET_MODE") == "text" {
			modes = []hardenllm.CallType{hardenllm.CallTypeText}
		}
		for _, mode := range modes {
			t.Run(id+"/"+string(mode), func(t *testing.T) {
				input := stateful.RunInput{ProfileID: id, ModelID: inputs.ModelID, UserPrompt: `Reply with exactly {"joke":"ok","explanation":"ok"}. No tools. No extra text.`, CallType: mode, Schema: inputs.Schema, ReasoningEffort: inputs.Reasoning, ProviderOptions: inputs.Options, CacheMode: hardenllm.CacheModeOff, TimeoutMS: 60000, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
				if mode == hardenllm.CallTypeText {
					input.Schema = nil
				}
				output, _, callErr := service.Run(context.Background(), scope, input)
				row := map[string]any{"id": id, "mode": string(mode), "status": output.Status, "providerInvoked": output.ProviderInvoked, "attemptCount": len(output.Attempts), "usedRepair": output.UsedRepair}
				if callErr != nil {
					problem := stateful.ProjectRunError(callErr)
					row["errorCode"] = problem.Code
					row["errorMessage"] = problem.Message
					row["attempts"] = output.Attempts
					row["diagnostics"] = output.Diagnostics
					t.Errorf("preset call failed: code=%s providerInvoked=%v", problem.Code, output.ProviderInvoked)
				} else if output.Status != "succeeded" || !output.ProviderInvoked || len(output.Attempts) != 1 || output.UsedRepair {
					t.Error("canary did not execute exactly one successful generation")
				}
				rows = append(rows, row)
				write()
			})
		}
	}
}
