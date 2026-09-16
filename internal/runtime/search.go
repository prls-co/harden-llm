package runtime

import (
	"errors"
	"fmt"
	"net/url"
	"unicode/utf8"
)

// SearchResult describes the evidence used to produce an answer. On a cache hit
// this is retained producer metadata, not a new search invocation.
type SearchResult struct {
	Mode           string           `json:"mode"`
	Executed       bool             `json:"executed"`
	Sources        []SearchSource   `json:"sources"`
	Citations      []SearchCitation `json:"citations,omitempty"`
	EntryPointHTML string           `json:"entryPointHtml,omitempty"`
	CostStatus     string           `json:"costStatus"`
}

type SearchCitation struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	StartIndex int    `json:"startIndex"`
	EndIndex   int    `json:"endIndex"`
}

type SearchSource struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

const maxSearchEntryPointCodePoints = 32768

// ValidSearchURL is the shared URL policy for normalized and persisted search
// evidence. It deliberately validates only the contract's transport facts;
// source uniqueness and citation membership are producer concerns.
func ValidSearchURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != "" && parsed.User == nil
}

// ValidateSearchResult validates the canonical search projection used by both
// fresh provider results and cache replay.
func ValidateSearchResult(result *SearchResult) error {
	if result == nil {
		return nil
	}
	if result.Mode != "native" && result.Mode != "jina" {
		return errors.New("runtime: search mode is invalid")
	}
	if result.CostStatus != "unavailable" {
		return errors.New("runtime: search cost status is invalid")
	}
	if result.Sources == nil || len(result.Sources) > 50 {
		return errors.New("runtime: search source list is invalid")
	}
	for _, source := range result.Sources {
		if !ValidSearchURL(source.URL) {
			return errors.New("runtime: search source URL is invalid")
		}
	}
	for _, citation := range result.Citations {
		if !ValidSearchURL(citation.URL) || citation.StartIndex < 0 || citation.EndIndex <= citation.StartIndex {
			return errors.New("runtime: search citation is invalid")
		}
	}
	if utf8.RuneCountInString(result.EntryPointHTML) > maxSearchEntryPointCodePoints {
		return fmt.Errorf("runtime: search entry point exceeds %d code points", maxSearchEntryPointCodePoints)
	}
	return nil
}
