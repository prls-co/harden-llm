package smoke

import (
	"bytes"
	"encoding/json"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-410
func hasCorrelatedResponseLog(body []byte, traceID string) bool {
	if traceID == "" {
		return false
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Labels map[string]string   `json:"stream"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Status != "success" {
		return false
	}
	for _, stream := range response.Data.Result {
		if stream.Labels["route"] != "/v1/responses" || stream.Labels["trace_id"] != traceID {
			continue
		}
		for _, entry := range stream.Values {
			if len(entry) >= 2 && bytes.Contains(entry[1], []byte("http request completed")) {
				return true
			}
		}
	}
	return false
}
