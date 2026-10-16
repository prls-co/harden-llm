package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/prls-co/harden-llm/api"
)

func (client *Client) ListProfiles(ctx context.Context) (ProfilesEnvelope, error) {
	var result ProfilesEnvelope
	err := client.get(ctx, "/api/v1/profiles", "ProfilesEnvelope", &result)
	return result, err
}

func (client *Client) GetProfile(ctx context.Context, id string) (ProfileDraft, error) {
	var result ProfileDraft
	data, err := json.Marshal(id)
	if err != nil || api.Validate("ReferenceProfileID", data) != nil {
		return result, errors.New("harden HTTP profile selector is invalid")
	}
	err = client.get(ctx, "/api/v1/profiles/"+url.PathEscape(id), "ProfileDraft", &result)
	return result, err
}

// ResolveReasoningEffort projects a validated profile's portable capabilities.
// An explicit choice must be configured. An omitted choice uses preferred
// when supported, then configured portable order; no map means no override.
// Callers own their preference and freezing this result with an accepted job.
// This function performs no HTTP, provider work or mutation of the definition.
func ResolveReasoningEffort(definition ProfileDefinition, requested, preferred string) (string, error) {
	for _, value := range []string{requested, preferred} {
		switch value {
		case "", "lowest", "middle", "highest":
		default:
			return "", errors.New("Harden reasoning effort must use a portable level")
		}
	}
	if requested != "" {
		if _, configured := definition.ReasoningEffortMap[requested]; !configured {
			return "", errors.New("the selected Harden profile does not configure the requested reasoning effort")
		}
		return requested, nil
	}
	if _, configured := definition.ReasoningEffortMap[preferred]; preferred != "" && configured {
		return preferred, nil
	}
	for _, value := range []string{"lowest", "middle", "highest"} {
		if _, configured := definition.ReasoningEffortMap[value]; configured {
			return value, nil
		}
	}
	return "", nil
}

func (client *Client) get(ctx context.Context, path, schemaName string, result any) error {
	response, err := client.request(ctx, http.MethodGet, path, "application/json", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return rejection(response)
	}
	if mediaType(response) != "application/json" {
		return &ProtocolError{}
	}
	data, err := readBounded(response.Body, maximumResponseBytes)
	if err != nil {
		return err
	}
	return decodeWire(schemaName, data, result)
}
