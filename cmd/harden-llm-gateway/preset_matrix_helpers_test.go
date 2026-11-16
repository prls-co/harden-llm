package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-455 TEST-548
// Shared by the opt-in live matrix and its credential-free regression tests.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/stateful"
)

func livePresetInput(id string, preset stateful.Preset, mode hardenllm.CallType, search bool) (stateful.RunInput, string, error) {
	var inputs struct {
		ModelID   string          `json:"modelId"`
		Schema    json.RawMessage `json:"schema"`
		Reasoning string          `json:"reasoningEffort"`
		Options   map[string]any  `json:"providerOptions"`
	}
	if err := json.Unmarshal(preset.Inputs, &inputs); err != nil {
		return stateful.RunInput{}, "", err
	}
	var profile stateful.Profile
	if err := json.Unmarshal(preset.Definition, &profile); err != nil {
		return stateful.RunInput{}, "", err
	}
	input := stateful.RunInput{ProfileID: id, ModelID: inputs.ModelID, UserPrompt: `Reply with exactly {"joke":"ok","explanation":"ok"}. No tools. No extra text.`, CallType: mode, Schema: inputs.Schema, ReasoningEffort: inputs.Reasoning, ProviderOptions: inputs.Options, CacheMode: hardenllm.CacheModeOff, TimeoutMS: 60000, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
	if mode == hardenllm.CallTypeText {
		input.Schema = nil
	}
	route := "off"
	if search {
		input.WebSearch = true
		input.UserPrompt = "Search the web for the official Go programming language documentation at go.dev/doc. Briefly describe what it covers and cite the source."
		if mode == hardenllm.CallTypeStructured {
			input.UserPrompt += ` Return only the configured JSON object: a short Go-related joke in "joke" and the documentation description with its URL in "explanation".`
		}
		route = "jina"
		if stateful.NativeWebSearchSupported(profile) {
			switch profile.APIInferenceType {
			case "responses", "gemini-generate-content":
				route = "native"
			case "anthropic-messages":
				if mode == hardenllm.CallTypeText {
					route = "native"
				}
			}
		}
	}
	return input, route, nil
}

func livePresetSearchFacts(output stateful.RunOutput, expected string) (map[string]any, error) {
	s := output.Search
	if s == nil || s.Mode != expected {
		return nil, errors.New("missing search evidence or unexpected native/fallback route")
	}
	if err := runtime.ValidateSearchResult(s); err != nil {
		return nil, errors.New("invalid canonical search evidence")
	}
	if expected == "jina" && !s.Executed {
		return nil, errors.New("configured Jina search did not execute")
	}
	observation := "not_observed"
	if s.Executed {
		observation = "observed"
	}
	facts := map[string]any{"searchMode": s.Mode, "searchExecuted": s.Executed, "searchObservation": observation, "searchCallCount": len(s.Calls), "sourceCount": len(s.Sources), "citationCount": len(s.Citations), "incomplete": s.Incomplete, "warnings": s.Warnings, "citationValidation": "no_citations"}
	if len(s.Citations) > 0 {
		// RunOutput intentionally does not expose the original assistant text
		// for structured output. Never marshal JSON and claim it is that text.
		facts["citationValidation"] = "original_text_unavailable"
		if text, ok := output.Output.(string); ok {
			sum := sha256.Sum256([]byte(text))
			if hex.EncodeToString(sum[:]) != s.CitationTextSHA256 {
				return facts, errors.New("citation hash does not match the returned text")
			}
			for _, citation := range s.Citations {
				if citation.EndIndex > utf8.RuneCountInString(text) {
					return facts, errors.New("citation range exceeds the returned text")
				}
			}
			facts["citationValidation"] = "verified"
		}
	}
	return facts, nil
}

func createLivePresetReport(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.WriteString("[]\n"); err != nil {
		return err
	}
	return f.Sync()
}

func writeLivePresetReport(path string, rows []map[string]any) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".preset-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
