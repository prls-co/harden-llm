package runtime

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"unicode/utf8"
)

// SearchResult describes the evidence used to produce an answer. On a cache hit
// this is retained producer metadata, not a new search invocation.
type SearchResult struct {
	Mode               string           `json:"mode"`
	Executed           bool             `json:"executed"`
	Sources            []SearchSource   `json:"sources"`
	Citations          []SearchCitation `json:"citations,omitempty"`
	Calls              []SearchCall     `json:"calls,omitempty"`
	CitationTextSHA256 string           `json:"citationTextSha256,omitempty"`
	Incomplete         bool             `json:"incomplete,omitempty"`
	// Warnings are unique, finite omission reasons. Nonempty warnings imply
	// Incomplete; they never contain provider-controlled text.
	Warnings       []string `json:"warnings,omitempty"`
	EntryPointHTML string   `json:"entryPointHtml,omitempty"`
	CostStatus     string   `json:"costStatus"`
}

const (
	MaxSearchSources    = 50
	MaxSearchCalls      = 32
	MaxSearchSourceRefs = 200
	MaxSearchTextBytes  = 4096
	MaxSearchCitations  = MaxSearchSourceRefs
	MaxSearchWarnings   = 12
)

type SearchCall struct {
	ID      string         `json:"id,omitempty"`
	Status  string         `json:"status"`
	Action  *SearchAction  `json:"action,omitempty"`
	Sources []SearchSource `json:"sources,omitempty"`
}

type SearchAction struct {
	Type    string `json:"type"`
	Query   string `json:"query,omitempty"`
	URL     string `json:"url,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

var searchToken = regexp.MustCompile(`^[a-z_]{1,32}$`)

// ValidSearchCall defines which upstream call metadata can be retained.
func ValidSearchCall(call SearchCall) bool {
	if !searchToken.MatchString(call.Status) {
		return false
	}
	if call.Action == nil {
		return true
	}
	a := call.Action
	return searchToken.MatchString(a.Type) && len(a.Query) <= MaxSearchTextBytes && len(a.Pattern) <= MaxSearchTextBytes && (a.URL == "" || ValidSearchURL(a.URL))
}

type SearchCitation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Indices are Unicode code points into the exact text named by
	// SearchResult.CitationTextSHA256, with an exclusive EndIndex.
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
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
	if len(result.Warnings) > MaxSearchWarnings {
		return errors.New("runtime: too many search warnings")
	}
	seenWarnings := make(map[string]bool, len(result.Warnings))
	for _, code := range result.Warnings {
		if !validSearchWarning(code) || seenWarnings[code] || !result.Incomplete {
			return errors.New("runtime: search warning is invalid")
		}
		seenWarnings[code] = true
	}
	if len(result.Citations) > MaxSearchCitations {
		return errors.New("runtime: too many search citations")
	}
	if result.Sources == nil || len(result.Sources) > MaxSearchSources {
		return errors.New("runtime: search source list is invalid")
	}
	sources := make(map[string]bool, len(result.Sources))
	for _, source := range result.Sources {
		if !ValidSearchURL(source.URL) {
			return errors.New("runtime: search source URL is invalid")
		}
		sources[source.URL] = true
	}
	if len(result.Calls) > MaxSearchCalls || (len(result.Citations) == 0) != (result.CitationTextSHA256 == "") {
		return errors.New("runtime: search provenance is invalid")
	}
	refs := 0
	for _, call := range result.Calls {
		if !ValidSearchCall(call) {
			return errors.New("runtime: search call is invalid")
		}
		refs += len(call.Sources)
		for _, source := range call.Sources {
			if !sources[source.URL] {
				return errors.New("runtime: search call source is invalid")
			}
		}
	}
	if refs > MaxSearchSourceRefs {
		return errors.New("runtime: too many search call sources")
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

func validSearchWarning(code string) bool {
	switch code {
	case "invalid_source", "source_limit", "invalid_call", "call_limit",
		"source_reference_limit", "unsupported_source_type", "invalid_citation",
		"unsupported_citation_type", "citation_text_unavailable", "citation_limit",
		"malformed_metadata", "entry_point_truncated":
		return true
	default:
		return false
	}
}
