package stateful

import (
	"bytes"
	_ "embed"
	"encoding/json"
)

// presetsJSON is moved credential-free metadata from baseline 754690c, changed to v4 complete templates.
//
//go:embed presets.json
var presetsJSON []byte

func DefaultCatalog() (Catalog, error) { return ParseCatalog(presetsJSON) }
func DefaultCatalogJSON() []byte       { return bytes.Clone(presetsJSON) }
func ParseCatalog(input []byte) (Catalog, error) {
	raw, err := decodeRawCatalog(input)
	if err != nil {
		return nil, validationFailure("catalog", "Catalog must be an object with unique IDs.")
	}
	result := make(Catalog, len(raw))
	for id, data := range raw {
		var p Preset
		if err := decodeStrict(data, &p); err != nil {
			return nil, validationFailure("catalog", "Template has an unknown field or invalid value.")
		}
		result[id] = p
	}
	if err := ValidateCatalog(result); err != nil {
		return nil, err
	}
	return result, nil
}
func ValidateCatalog(catalog Catalog) error {
	if catalog == nil {
		return validationFailure("catalog", "Catalog must be an object.")
	}
	for id, p := range catalog {
		if err := validateProfileName(id); err != nil {
			return validationFailure("id", err.Error())
		}
		_, err := validateSnapshot(SaveProfileInput{Name: p.Name, PresetID: id, Definition: p.Definition, Inputs: p.Inputs, Binding: p.Binding})
		if err != nil {
			return err
		}
	}
	return nil
}
func definitionForValidation(model string) ProfileDraft {
	encoded, _ := json.Marshal(map[string]string{"modelId": model})
	return ProfileDraft{Definition: encoded}
}
