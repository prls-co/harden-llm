package providers

import (
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/runtime"
)

var searchURLPattern = regexp.MustCompile(`https?://[^\s<>"\x60]+`)

func normalizeSearch(prepared preparedRequest, response map[string]any, assistantText string) *runtime.SearchResult {
	if prepared.searchMode == "" {
		return nil
	}
	e := &searchEvidence{
		result: &runtime.SearchResult{Mode: prepared.searchMode, Sources: []runtime.SearchSource{}, CostStatus: "unavailable"},
		seen:   map[string]bool{}, text: assistantText,
	}
	result := e.result
	if prepared.webSearch != nil {
		prepared.webSearch.state.mu.Lock()
		context := prepared.webSearch.state.result
		prepared.webSearch.state.mu.Unlock()
		result.Executed = context != ""
		var sources []runtime.SearchSource
		for _, raw := range searchURLPattern.FindAllString(context, -1) {
			sources = append(sources, runtime.SearchSource{URL: strings.TrimRight(raw, ").,;]")})
		}
		if result.Executed {
			e.addCall(runtime.SearchCall{Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: prepared.webSearch.query}}, sources)
		}
	}
	final := finalResponseOutput(response)
	var finalItem any
	if len(final) > 0 {
		finalItem = final[0]
	}
	citationTextSeen := false
	for _, raw := range arrayValue(response["output"]) {
		item := objectValue(raw)
		if item["type"] == "web_search_call" {
			result.Executed = result.Executed || item["status"] == "completed"
			a := objectValue(item["action"])
			call := runtime.SearchCall{ID: stringValue(item["id"]), Status: stringValue(item["status"])}
			if item["action"] != nil {
				call.Action = &runtime.SearchAction{Type: stringValue(a["type"]), Query: stringValue(a["query"]), URL: stringValue(a["url"]), Pattern: stringValue(a["pattern"])}
			}
			var sources []runtime.SearchSource
			for _, source := range e.array(a["sources"]) {
				s := objectValue(source)
				if s["type"] != "url" {
					e.warn("unsupported_source_type")
					if url := stringValue(s["url"]); url != "" {
						e.addSource(url, stringValue(s["title"]))
					}
					continue
				}
				sources = append(sources, runtime.SearchSource{URL: stringValue(s["url"]), Title: stringValue(s["title"])})
			}
			e.addCall(call, sources)
		}
		if item["type"] == "search_results" {
			result.Executed = true
			for _, source := range e.array(item["results"]) {
				s := objectValue(source)
				e.addSource(stringValue(s["url"]), stringValue(s["title"]))
			}
		}
		for _, content := range arrayValue(item["content"]) {
			part := objectValue(content)
			firstText := !citationTextSeen && reflect.DeepEqual(item, finalItem) && strings.TrimSpace(stringValue(part["text"])) != ""
			for _, annotation := range e.array(part["annotations"]) {
				a := objectValue(annotation)
				if a["type"] != "url_citation" {
					e.warn("unsupported_citation_type")
					continue
				}
				url, title := stringValue(a["url"]), stringValue(a["title"])
				if !e.addSource(url, title) {
					continue
				}
				if !firstText || stringValue(part["text"]) != assistantText {
					e.warn("citation_text_unavailable")
					continue
				}
				start, startOK := searchIndex(a["start_index"], false)
				end, endOK := searchIndex(a["end_index"], false)
				if !startOK || !endOK {
					e.warn("invalid_citation")
					continue
				}
				e.addCitation(url, title, start, end)
			}
			citationTextSeen = citationTextSeen || firstText
		}
	}
	toolResults := make(map[string]map[string]any)
	for _, raw := range arrayValue(response["content"]) {
		item := objectValue(raw)
		if item["type"] == "web_search_tool_result" {
			toolResults[stringValue(item["tool_use_id"])] = item
		}
	}
	for _, raw := range arrayValue(response["content"]) {
		item := objectValue(raw)
		if item["type"] == "server_tool_use" && item["name"] == "web_search" {
			id := stringValue(item["id"])
			if block, ok := toolResults[id]; ok && id != "" {
				var sources []runtime.SearchSource
				for _, raw := range e.array(block["content"]) {
					value := objectValue(raw)
					sources = append(sources, runtime.SearchSource{URL: stringValue(value["url"]), Title: stringValue(value["title"])})
				}
				e.addCall(runtime.SearchCall{ID: id, Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: stringValue(objectValue(item["input"])["query"])}}, sources)
			} else {
				e.warn("invalid_call")
			}
		}
		if item["type"] == "web_search_tool_result" {
			result.Executed = true
		}
	}
	if prepared.protocol == "anthropic.messages" {
		e.anthropicCitations(arrayValue(response["content"]))
	}
	for i, raw := range arrayValue(response["candidates"]) {
		candidate := objectValue(raw)
		grounding, groundingOK := candidate["groundingMetadata"].(map[string]any)
		if candidate["groundingMetadata"] != nil && !groundingOK {
			e.warn("malformed_metadata")
		}
		entry := stringValue(objectValue(grounding["searchEntryPoint"])["renderedContent"])
		result.EntryPointHTML = boundedWebSearchText(entry, 32768)
		if utf8.RuneCountInString(entry) > 32768 {
			e.warn("entry_point_truncated")
		}
		queries := e.array(grounding["webSearchQueries"])
		result.Executed = result.Executed || len(queries) > 0
		for _, q := range queries {
			e.addCall(runtime.SearchCall{Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: stringValue(q)}}, nil)
		}
		for _, chunk := range e.array(grounding["groundingChunks"]) {
			web, webOK := objectValue(chunk)["web"].(map[string]any)
			if !webOK {
				e.warn("unsupported_source_type")
				continue
			}
			e.addSource(stringValue(web["uri"]), stringValue(web["title"]))
		}
		if prepared.protocol == "google.gemini.generateContent" {
			e.geminiCitations(candidate, i == 0)
		}
	}
	for _, source := range e.array(response["search_results"]) {
		s := objectValue(source)
		e.addSource(stringValue(s["url"]), stringValue(s["title"]))
	}
	for _, source := range e.array(response["citations"]) {
		e.addSource(stringValue(source), "")
	}
	return result
}

func finalResponseOutput(response map[string]any) []any {
	output := arrayValue(response["output"])
	for index := len(output) - 1; index >= 0; index-- {
		item := objectValue(output[index])
		if len(arrayValue(item["content"])) > 0 {
			return []any{output[index]}
		}
	}
	return output
}
