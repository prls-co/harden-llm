package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/runtime"
)

// searchEvidence admits optional provider metadata into the strict canonical
// projection. Its warnings contain finite codes, never provider-controlled text.
type searchEvidence struct {
	result *runtime.SearchResult
	text   string
	seen   map[string]bool
	refs   int
}

func (e *searchEvidence) warn(code string) {
	e.result.Incomplete = true
	for _, existing := range e.result.Warnings {
		if existing == code {
			return
		}
	}
	e.result.Warnings = append(e.result.Warnings, code)
}

func (e *searchEvidence) array(value any) []any {
	if value == nil {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	e.warn("malformed_metadata")
	return nil
}

func (e *searchEvidence) addSource(raw, title string) bool {
	if e.seen[raw] {
		return true
	}
	if !runtime.ValidSearchURL(raw) {
		e.warn("invalid_source")
		return false
	}
	if len(e.result.Sources) >= runtime.MaxSearchSources {
		e.warn("source_limit")
		return false
	}
	e.seen[raw] = true
	if title == "" {
		title = raw
	}
	e.result.Sources = append(e.result.Sources, runtime.SearchSource{URL: raw, Title: title})
	return true
}

func (e *searchEvidence) addCall(call runtime.SearchCall, sources []runtime.SearchSource) {
	if len(e.result.Calls) >= runtime.MaxSearchCalls || !runtime.ValidSearchCall(call) {
		if len(e.result.Calls) >= runtime.MaxSearchCalls {
			e.warn("call_limit")
		} else {
			e.warn("invalid_call")
		}
		// Preserve known sources without inventing a call association.
		for _, source := range sources {
			e.addSource(source.URL, source.Title)
		}
		return
	}
	local := map[string]bool{}
	for _, source := range sources {
		if local[source.URL] {
			continue
		}
		local[source.URL] = true
		if !e.addSource(source.URL, source.Title) {
			continue
		}
		if e.refs >= runtime.MaxSearchSourceRefs {
			e.warn("source_reference_limit")
			continue
		}
		if source.Title == "" {
			source.Title = source.URL
		}
		call.Sources = append(call.Sources, source)
		e.refs++
	}
	e.result.Calls = append(e.result.Calls, call)
}

func (e *searchEvidence) addCitation(url, title string, start, end int) {
	if !e.addSource(url, title) {
		return
	}
	if start < 0 || end <= start || end > utf8.RuneCountInString(e.text) {
		e.warn("invalid_citation")
		return
	}
	for _, c := range e.result.Citations {
		if c.URL == url && c.StartIndex == start && c.EndIndex == end {
			return
		}
	}
	if len(e.result.Citations) >= runtime.MaxSearchCitations {
		e.warn("citation_limit")
		return
	}
	e.result.Citations = append(e.result.Citations, runtime.SearchCitation{URL: url, Title: title, StartIndex: start, EndIndex: end})
	if e.result.CitationTextSHA256 == "" {
		sum := sha256.Sum256([]byte(e.text))
		e.result.CitationTextSHA256 = hex.EncodeToString(sum[:])
	}
}
