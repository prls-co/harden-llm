package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/prls-co/harden-llm/internal/retry"
)

type RepairMetadata struct {
	Explanation string
	Changes     []string
}

func RepairEligible(attempt, maxAttempts int, classification retry.Classification, hasSchema bool) bool {
	return hasSchema && attempt >= 1 && attempt < maxAttempts && classification.Retryable && classification.Category == retry.CategoryParse
}

func ExtractRepairData(raw json.RawMessage, validate func(json.RawMessage) error) (json.RawMessage, RepairMetadata, error) {
	type repairDetails struct {
		Explanation string   `json:"explanation"`
		Changes     []string `json:"changes"`
	}
	type envelope struct {
		Repair repairDetails   `json:"repair"`
		Data   json.RawMessage `json:"data"`
	}
	var parsed envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, RepairMetadata{}, fmt.Errorf("invalid repair envelope: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, RepairMetadata{}, err
	}
	if parsed.Repair.Explanation == "" || parsed.Repair.Changes == nil || len(parsed.Data) == 0 || bytes.Equal(parsed.Data, []byte("null")) {
		return nil, RepairMetadata{}, errors.New("invalid repair envelope: repair.explanation, repair.changes, and data are required")
	}
	if validate == nil {
		return nil, RepairMetadata{}, errors.New("repair data validator is required")
	}
	if err := validate(parsed.Data); err != nil {
		return nil, RepairMetadata{}, fmt.Errorf("repair data validation: %w", err)
	}
	return append(json.RawMessage(nil), parsed.Data...), RepairMetadata{
		Explanation: parsed.Repair.Explanation,
		Changes:     append([]string(nil), parsed.Repair.Changes...),
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid repair envelope: %w", err)
	}
	return errors.New("invalid repair envelope: trailing JSON value")
}
