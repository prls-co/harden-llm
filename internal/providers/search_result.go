package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"strings"

	"github.com/prls-co/harden-llm/internal/runtime"
)

var searchURLPattern = regexp.MustCompile(`https?://[^\s<>"\x60]+`)

func normalizeSearch(prepared preparedRequest, response map[string]any) *runtime.SearchResult {
	if prepared.searchMode == "" {
		return nil
	}
	result := &runtime.SearchResult{Mode: prepared.searchMode, Sources: []runtime.SearchSource{}, CostStatus: "unavailable"}
	seen := map[string]bool{}
	add := func(raw, title string) bool {
		if seen[raw] {
			return true
		}
		if !runtime.ValidSearchURL(raw) || len(result.Sources) >= runtime.MaxSearchSources {
			result.Incomplete = true
			return false
		}
		seen[raw] = true
		if title == "" {
			title = raw
		}
		result.Sources = append(result.Sources, runtime.SearchSource{URL: raw, Title: title})
		return true
	}
	refs := 0
	addCall := func(call runtime.SearchCall, sources []runtime.SearchSource) {
		if len(result.Calls) >= runtime.MaxSearchCalls || !runtime.ValidSearchCall(call) {
			result.Incomplete = true
			// Source evidence remains useful even when its call cannot be
			// represented; do not invent an association for it.
			for _, source := range sources {
				add(source.URL, source.Title)
			}
			return
		}
		local := map[string]bool{}
		for _, source := range sources {
			if local[source.URL] {
				continue
			}
			local[source.URL] = true
			if !add(source.URL, source.Title) {
				continue
			}
			if refs >= runtime.MaxSearchSourceRefs {
				result.Incomplete = true
				continue
			}
			if source.Title == "" {
				source.Title = source.URL
			}
			call.Sources = append(call.Sources, source)
			refs++
		}
		result.Calls = append(result.Calls, call)
	}
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
			addCall(runtime.SearchCall{Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: prepared.webSearch.query}}, sources)
		}
	}
	final := finalResponseOutput(response)
	var finalItem any
	if len(final) > 0 {
		finalItem = final[0]
	}
	citationTextSeen := false
	for _, item := range arrayValue(response["output"]) {
		item := objectValue(item)
		if item["type"] == "web_search_call" {
			result.Executed = result.Executed || item["status"] == "completed"
			a := objectValue(item["action"])
			call := runtime.SearchCall{ID: stringValue(item["id"]), Status: stringValue(item["status"])}
			if item["action"] != nil {
				call.Action = &runtime.SearchAction{Type: stringValue(a["type"]), Query: stringValue(a["query"]), URL: stringValue(a["url"]), Pattern: stringValue(a["pattern"])}
			}
			var sources []runtime.SearchSource
			for _, source := range arrayValue(a["sources"]) {
				s := objectValue(source)
				if s["type"] != "url" {
					result.Incomplete = true
					if url := stringValue(s["url"]); url != "" {
						add(url, stringValue(s["title"]))
					}
					continue
				}
				sources = append(sources, runtime.SearchSource{URL: stringValue(s["url"]), Title: stringValue(s["title"])})
			}
			addCall(call, sources)
		}
		if item["type"] == "search_results" {
			result.Executed = true
			for _, source := range arrayValue(item["results"]) {
				s := objectValue(source)
				add(stringValue(s["url"]), stringValue(s["title"]))
			}
		}
		for _, content := range arrayValue(item["content"]) {
			part := objectValue(content)
			firstText := !citationTextSeen && reflect.DeepEqual(item, finalItem) && strings.TrimSpace(stringValue(part["text"])) != ""
			for _, annotation := range arrayValue(objectValue(content)["annotations"]) {
				a := objectValue(annotation)
				if a["type"] == "url_citation" {
					add(stringValue(a["url"]), stringValue(a["title"]))
					start, end := int(integerValue(a["start_index"])), int(integerValue(a["end_index"]))
					if firstText && seen[stringValue(a["url"])] && start >= 0 && end > start && end <= len([]rune(stringValue(part["text"]))) {
						result.Citations = append(result.Citations, runtime.SearchCitation{URL: stringValue(a["url"]), Title: stringValue(a["title"]), StartIndex: start, EndIndex: end})
						digest := sha256.Sum256([]byte(stringValue(part["text"])))
						result.CitationTextSHA256 = hex.EncodeToString(digest[:])
					}
				}
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
	for _, item := range arrayValue(response["content"]) {
		item := objectValue(item)
		if item["type"] == "server_tool_use" && item["name"] == "web_search" {
			id := stringValue(item["id"])
			if resultBlock, ok := toolResults[id]; ok && id != "" {
				var sources []runtime.SearchSource
				for _, raw := range arrayValue(resultBlock["content"]) {
					value := objectValue(raw)
					sources = append(sources, runtime.SearchSource{URL: stringValue(value["url"]), Title: stringValue(value["title"])})
				}
				addCall(runtime.SearchCall{ID: id, Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: stringValue(objectValue(item["input"])["query"])}}, sources)
			} else {
				result.Incomplete = true
			}
		}
		if item["type"] == "web_search_tool_result" {
			result.Executed = true
		}
		for _, citation := range arrayValue(item["citations"]) {
			c := objectValue(citation)
			if c["type"] == "web_search_result_location" {
				add(stringValue(c["url"]), stringValue(c["title"]))
			}
		}
	}
	for _, candidate := range arrayValue(response["candidates"]) {
		grounding := objectValue(objectValue(candidate)["groundingMetadata"])
		result.EntryPointHTML = boundedWebSearchText(stringValue(objectValue(grounding["searchEntryPoint"])["renderedContent"]), 32768)
		result.Executed = result.Executed || len(arrayValue(grounding["webSearchQueries"])) > 0
		for _, q := range arrayValue(grounding["webSearchQueries"]) {
			addCall(runtime.SearchCall{Status: "completed", Action: &runtime.SearchAction{Type: "search", Query: stringValue(q)}}, nil)
		}
		for _, chunk := range arrayValue(grounding["groundingChunks"]) {
			web := objectValue(objectValue(chunk)["web"])
			add(stringValue(web["uri"]), stringValue(web["title"]))
		}
	}
	for _, source := range arrayValue(response["search_results"]) {
		s := objectValue(source)
		add(stringValue(s["url"]), stringValue(s["title"]))
	}
	for _, source := range arrayValue(response["citations"]) {
		add(stringValue(source), "")
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
