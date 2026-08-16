package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/runtime"
)

const (
	maximumModelPageBytes  = 2 << 20
	maximumModelPages      = 20
	maximumModels          = 5000
	maximumModelIDBytes    = 512
	maximumModelLabelBytes = 1024
)

// ModelDiscoveryRequest contains the minimum origin-bound provider state used
// by explicit model refresh. Credentials are never returned or logged.
type ModelDiscoveryRequest struct {
	BaseURL          string
	APIInferenceType string
	APIKey           string
	Headers          map[string]string
}

// DiscoveredModel is a normalized provider model projection.
type DiscoveredModel struct {
	ID    string
	Label string
}

// ModelDiscovery reuses the provider SSRF guard and bounded HTTP transport.
type ModelDiscovery struct{ client *http.Client }

func NewModelDiscovery(policy EndpointPolicy) (*ModelDiscovery, error) {
	client, err := newSafeHTTPClient(policy)
	if err != nil {
		return nil, err
	}
	return &ModelDiscovery{client: client}, nil
}

func (discovery *ModelDiscovery) Discover(ctx context.Context, input ModelDiscoveryRequest) ([]DiscoveredModel, error) {
	if discovery == nil || discovery.client == nil || ctx == nil {
		return nil, errors.New("providers: model discovery is not initialized")
	}
	operationPath := "/models"
	if input.APIInferenceType == "gemini-generate-content" {
		operationPath = "/v1beta/models"
	}
	if input.APIInferenceType != "responses" && input.APIInferenceType != "chat-completions" &&
		input.APIInferenceType != "gemini-generate-content" && input.APIInferenceType != "anthropic-messages" {
		return nil, errors.New("providers: model discovery protocol is unsupported")
	}
	_, endpoint, err := endpointURL(input.BaseURL, input.APIInferenceType, operationPath)
	if err != nil {
		return nil, errors.New("providers: model discovery endpoint is invalid")
	}
	headers, err := providerHeaders(input.APIInferenceType, runtime.Credential{APIKey: input.APIKey, Headers: input.Headers})
	if err != nil {
		return nil, err
	}
	headers.Del("Content-Type")

	models := make(map[string]DiscoveredModel)
	seenCursors := make(map[string]struct{})
	cursor := ""
	for page := 0; page < maximumModelPages; page++ {
		pageURL := cloneURL(endpoint)
		setModelPageQuery(pageURL, input.APIInferenceType, cursor)
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
		if requestErr != nil {
			return nil, errors.New("providers: model discovery request is invalid")
		}
		request.Header = headers.Clone()
		response, requestErr := discovery.client.Do(request)
		if requestErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("providers: model discovery request failed")
		}
		body, readErr := readModelResponse(response)
		if readErr != nil {
			return nil, readErr
		}
		pageModels, nextCursor, decodeErr := decodeModelPage(input.APIInferenceType, body)
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, model := range pageModels {
			model, valid := normalizeDiscoveredModel(model)
			if !valid {
				continue
			}
			models[model.ID] = model
			if len(models) > maximumModels {
				return nil, errors.New("providers: model discovery exceeded the model limit")
			}
		}
		if nextCursor == "" {
			return sortedDiscoveredModels(models), nil
		}
		if len(nextCursor) > 4096 {
			return nil, errors.New("providers: model discovery cursor is invalid")
		}
		if _, duplicate := seenCursors[nextCursor]; duplicate {
			return nil, errors.New("providers: model discovery pagination did not advance")
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return nil, errors.New("providers: model discovery exceeded the page limit")
}

func readModelResponse(response *http.Response) ([]byte, error) {
	if response == nil {
		return nil, errors.New("providers: model discovery returned no response")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("providers: model discovery returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("providers: model discovery returned an invalid content type")
	}
	body, err := readBounded(response.Body, maximumModelPageBytes)
	if err != nil {
		return nil, errors.New("providers: model discovery response exceeded its limit")
	}
	return body, nil
}

func decodeModelPage(inferenceType string, body []byte) ([]DiscoveredModel, string, error) {
	if inferenceType == "gemini-generate-content" {
		var document struct {
			Models []struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if json.Unmarshal(body, &document) != nil || document.Models == nil {
			return nil, "", errors.New("providers: model discovery response is invalid")
		}
		result := make([]DiscoveredModel, 0, len(document.Models))
		for _, model := range document.Models {
			result = append(result, DiscoveredModel{ID: strings.TrimPrefix(model.Name, "models/"), Label: model.DisplayName})
		}
		return result, strings.TrimSpace(document.NextPageToken), nil
	}
	var document struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if json.Unmarshal(body, &document) != nil || document.Data == nil {
		return nil, "", errors.New("providers: model discovery response is invalid")
	}
	result := make([]DiscoveredModel, 0, len(document.Data))
	for _, model := range document.Data {
		result = append(result, DiscoveredModel{ID: model.ID, Label: model.DisplayName})
	}
	if !document.HasMore {
		return result, "", nil
	}
	lastID := strings.TrimSpace(document.LastID)
	if lastID == "" && len(document.Data) > 0 {
		lastID = strings.TrimSpace(document.Data[len(document.Data)-1].ID)
	}
	if lastID == "" {
		return nil, "", errors.New("providers: model discovery pagination cursor is missing")
	}
	return result, lastID, nil
}

func setModelPageQuery(endpoint *url.URL, inferenceType, cursor string) {
	query := endpoint.Query()
	if inferenceType == "gemini-generate-content" {
		query.Set("pageSize", "1000")
		if cursor != "" {
			query.Set("pageToken", cursor)
		}
	} else if cursor != "" {
		if inferenceType == "anthropic-messages" {
			query.Set("after_id", cursor)
		} else {
			query.Set("after", cursor)
		}
	}
	endpoint.RawQuery = query.Encode()
}

func cloneURL(input *url.URL) *url.URL {
	cloned := *input
	return &cloned
}

func normalizeDiscoveredModel(model DiscoveredModel) (DiscoveredModel, bool) {
	model.ID = strings.TrimSpace(model.ID)
	model.Label = strings.TrimSpace(model.Label)
	if model.Label == "" {
		model.Label = model.ID
	}
	if model.ID == "" || !utf8.ValidString(model.ID) || !utf8.ValidString(model.Label) ||
		len(model.ID) > maximumModelIDBytes || len(model.Label) > maximumModelLabelBytes {
		return DiscoveredModel{}, false
	}
	return model, true
}

func sortedDiscoveredModels(models map[string]DiscoveredModel) []DiscoveredModel {
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]DiscoveredModel, 0, len(ids))
	for _, id := range ids {
		result = append(result, models[id])
	}
	return result
}
