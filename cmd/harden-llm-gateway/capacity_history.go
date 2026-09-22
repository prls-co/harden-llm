package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/prls-co/harden-llm/internal/capacity"
)

const (
	capacityToken                   = "synthetic-capacity-gateway-token-0123456789abcdef"
	capacityHistoryPageLimit        = 100
	maxCapacityHistoryPages         = 128
	maxCapacityHistoryResponseBytes = 2 << 20
)

type capacityHistoryPage struct {
	Result struct {
		Items []struct {
			RunID   string `json:"runId"`
			TraceID string `json:"traceId"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	} `json:"result"`
}

func assertCapacityHistory(ctx context.Context, client *http.Client, gatewayURL string, requests []capacity.RequestResult) error {
	if len(requests) == 0 {
		return nil
	}
	endpoint, err := url.Parse(strings.TrimRight(gatewayURL, "/") + "/api/v1/history")
	if err != nil {
		return fmt.Errorf("parse REST history endpoint: %w", err)
	}
	pending := make(map[string]string, len(requests))
	for _, expected := range requests {
		if existing, exists := pending[expected.RunID]; exists && existing != expected.TraceID {
			return fmt.Errorf("expected history contains conflicting traces for run %s", expected.RunID)
		}
		pending[expected.RunID] = expected.TraceID
	}
	seenCursors := make(map[string]struct{})
	cursor := ""
	for pageNumber := 0; pageNumber < maxCapacityHistoryPages; pageNumber++ {
		pageURL := *endpoint
		query := pageURL.Query()
		query.Set("limit", fmt.Sprint(capacityHistoryPageLimit))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		pageURL.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
		if err != nil {
			return fmt.Errorf("create REST history page request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+capacityToken)
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("request REST history page %d: %w", pageNumber+1, err)
		}
		content, readErr := io.ReadAll(io.LimitReader(response.Body, maxCapacityHistoryResponseBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read REST history page %d: %w", pageNumber+1, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close REST history page %d: %w", pageNumber+1, closeErr)
		}
		if len(content) > maxCapacityHistoryResponseBytes {
			return fmt.Errorf("REST history page %d exceeded the %d-byte response limit", pageNumber+1, maxCapacityHistoryResponseBytes)
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("REST history endpoint returned HTTP %d on page %d", response.StatusCode, pageNumber+1)
		}
		var page capacityHistoryPage
		if err := json.Unmarshal(content, &page); err != nil {
			return fmt.Errorf("decode REST history page %d: %w", pageNumber+1, err)
		}
		for _, item := range page.Result.Items {
			if traceID, expected := pending[item.RunID]; expected && traceID == item.TraceID {
				delete(pending, item.RunID)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		nextCursor := page.Result.NextCursor
		if nextCursor == "" {
			for runID, traceID := range pending {
				return fmt.Errorf("REST history omitted persisted run/trace pair %s/%s", runID, traceID)
			}
			return nil
		}
		if len(nextCursor) > 512 {
			return fmt.Errorf("REST history next cursor exceeds the 512-character contract limit")
		}
		if _, repeated := seenCursors[nextCursor]; repeated {
			return fmt.Errorf("REST history pagination repeated a cursor")
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return fmt.Errorf("REST history pagination exceeded %d pages with %d expected pairs unresolved", maxCapacityHistoryPages, len(pending))
}
