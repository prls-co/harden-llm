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
