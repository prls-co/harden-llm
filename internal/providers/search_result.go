package providers

import (
	"net/url"
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
	add := func(raw, title string) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || seen[raw] || len(result.Sources) >= 50 {
			return
		}
		seen[raw] = true
		if title == "" {
			title = raw
		}
		result.Sources = append(result.Sources, runtime.SearchSource{URL: raw, Title: title})
	}
	if prepared.webSearch != nil {
		prepared.webSearch.state.mu.Lock()
		context := prepared.webSearch.state.result
		prepared.webSearch.state.mu.Unlock()
		result.Executed = context != ""
		for _, raw := range searchURLPattern.FindAllString(context, 50) {
			add(strings.TrimRight(raw, ").,;]"), "")
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
			for _, source := range arrayValue(objectValue(item["action"])["sources"]) {
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
					if firstText && prepared.callType == "text" && seen[stringValue(a["url"])] && start >= 0 && end > start && end <= len([]rune(stringValue(part["text"]))) {
						result.Citations = append(result.Citations, runtime.SearchCitation{URL: stringValue(a["url"]), Title: stringValue(a["title"]), StartIndex: start, EndIndex: end})
					}
				}
			}
			citationTextSeen = citationTextSeen || firstText
		}
	}
	for _, item := range arrayValue(response["content"]) {
		item := objectValue(item)
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
	if strings.EqualFold(prepared.provider, "perplexity") {
		result.Executed = len(result.Sources) > 0
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
