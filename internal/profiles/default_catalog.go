package profiles

import (
	"bytes"
	_ "embed"
)

// defaultProfileCatalogJSON is the credential-free utility-llm profile seed.
// It is embedded so the gateway has one immutable catalog source and does not
// depend on the source checkout or a second runtime configuration file.
//
//go:embed default-profile-catalog.json
var defaultProfileCatalogJSON []byte

// DefaultCatalog returns a validated copy of the built-in profile seed.
func DefaultCatalog() (Catalog, error) {
	return ParseCatalog(bytes.Clone(defaultProfileCatalogJSON))
}

// DefaultCatalogJSON returns a copy of the embedded seed artifact for release
// and provenance checks without exposing mutable package state.
func DefaultCatalogJSON() []byte {
	return bytes.Clone(defaultProfileCatalogJSON)
}
