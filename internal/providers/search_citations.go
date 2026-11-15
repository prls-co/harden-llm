package providers

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Metadata indices are integers, not coercible strings or truncated floats.
func searchIndex(value any, defaultZero bool) (int, bool) {
	if value == nil {
		return 0, defaultZero
	}
	var raw string
	switch value := value.(type) {
	case json.Number:
		raw = string(value)
	case int:
		raw = strconv.Itoa(value)
	case int64:
		raw = strconv.FormatInt(value, 10)
	case float64:
		raw = strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, strconv.IntSize)
	return int(n), err == nil && n >= 0
}

func (e *searchEvidence) anthropicCitations(content []any) {
	var joined strings.Builder
	for _, raw := range content {
		part := objectValue(raw)
		if part["type"] == "text" {
			joined.WriteString(stringValue(part["text"]))
		}
	}
	offset := 0
	for _, raw := range content {
		part := objectValue(raw)
		if part["type"] != "text" {
			continue
		}
		text := stringValue(part["text"])
		end := offset + utf8.RuneCountInString(text)
		for _, rawCitation := range e.array(part["citations"]) {
			citation := objectValue(rawCitation)
			if citation["type"] != "web_search_result_location" {
				e.warn("unsupported_citation_type")
				continue
			}
			url, title := stringValue(citation["url"]), stringValue(citation["title"])
			if !e.addSource(url, title) {
				continue
			}
			if joined.String() != e.text {
				e.warn("citation_text_unavailable")
				continue
			}
			// cited_text quotes the source; the citation supports this answer block.
			e.addCitation(url, title, offset, end)
		}
		offset = end
	}
}

func (e *searchEvidence) geminiCitations(candidate map[string]any, selected bool) {
	grounding := objectValue(candidate["groundingMetadata"])
	chunks := e.array(grounding["groundingChunks"])
	parts := arrayValue(objectValue(candidate["content"])["parts"])
	selectedPart := -1
	for i, raw := range parts {
		part := objectValue(raw)
		if part["functionCall"] == nil && stringValue(part["text"]) != "" {
			selectedPart = i
			break
		}
	}
	for _, raw := range e.array(grounding["groundingSupports"]) {
		support := objectValue(raw)
		segment := objectValue(support["segment"])
		partIndex, partOK := searchIndex(segment["partIndex"], true)
		start, startOK := searchIndex(segment["startIndex"], true)
		end, endOK := searchIndex(segment["endIndex"], false)
		if !partOK || !startOK || !endOK || end <= start || partIndex >= len(parts) {
			e.warn("invalid_citation")
			continue
		}
		if !selected || partIndex != selectedPart {
			e.warn("citation_text_unavailable")
			continue
		}
		text := stringValue(objectValue(parts[partIndex])["text"])
		if text != e.text {
			e.warn("citation_text_unavailable")
			continue
		}
		if end > len(text) || !utf8.ValidString(text[:start]) || !utf8.ValidString(text[start:end]) {
			e.warn("invalid_citation")
			continue
		}
		if rawText, present := segment["text"]; present {
			if actual, ok := rawText.(string); !ok || actual != text[start:end] {
				e.warn("invalid_citation")
				continue
			}
		}
		indices := e.array(support["groundingChunkIndices"])
		if len(indices) == 0 {
			e.warn("invalid_citation")
		}
		for _, rawIndex := range indices {
			index, ok := searchIndex(rawIndex, false)
			if !ok || index >= len(chunks) {
				e.warn("invalid_citation")
				continue
			}
			web, webOK := objectValue(chunks[index])["web"].(map[string]any)
			if !webOK {
				e.warn("unsupported_source_type")
				continue
			}
			e.addCitation(stringValue(web["uri"]), stringValue(web["title"]), utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:end]))
		}
	}
}
