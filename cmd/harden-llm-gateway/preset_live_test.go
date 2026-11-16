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
			input, route, err := livePresetInput(id, preset, mode, search == "1")
			if err != nil {
				t.Fatal(err)
			}
			var profile stateful.Profile
			if err = json.Unmarshal(preset.Definition, &profile); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": id, "mode": string(mode), "model": input.ModelID, "provider": profile.Provider, "protocol": profile.APIInferenceType, "status": "planned", "searchRequested": input.WebSearch, "expectedSearchMode": route}
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
			row["status"], row["providerInvoked"], row["attemptCount"], row["usedRepair"] = output.Status, output.ProviderInvoked, len(output.Attempts), output.UsedRepair
			row["durationMs"], row["cacheServed"], row["finishedAt"] = output.TotalCallDurationMs, output.Cache.Served, time.Now().UTC().Format(time.RFC3339Nano)
			statuses := []int{}
			for _, attempt := range output.Attempts {
				statuses = append(statuses, attempt.HTTPStatus)
				if callErr != nil && (attempt.DispatchObserved || attempt.ProviderUsed) && (attempt.Category == "network" || attempt.Category == "timeout" || attempt.Category == "canceled") {
					row["status"] = "unconfirmed"
				}
			}
			row["httpStatuses"] = statuses
			var evidenceErr error
			if input.WebSearch && (callErr == nil || output.Search != nil) {
				facts, err := livePresetSearchFacts(output, row["expectedSearchMode"].(string))
				for key, value := range facts {
					row[key] = value
				}
				evidenceErr = err
				if err != nil {
					row["evidenceError"] = err.Error()
				}
			}
			if callErr != nil {
				problem := stateful.ProjectRunError(callErr)
				row["errorCode"] = problem.Code
				if problem.Code == "execution_unconfirmed" {
					row["status"] = "unconfirmed"
				}
				t.Errorf("preset call failed: code=%s providerInvoked=%v", problem.Code, output.ProviderInvoked)
			} else if output.Status != "succeeded" || !output.ProviderInvoked || len(output.Attempts) != 1 || output.UsedRepair || output.Cache.Served {
				row["status"] = "invalid_execution"
				t.Error("canary did not execute exactly one successful generation")
			} else if evidenceErr != nil {
				row["status"] = "invalid_evidence"
				t.Error(evidenceErr)
			}
		})
		if reportErr != nil {
			t.Fatal(reportErr)
		}
		write()
	}
}
