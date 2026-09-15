package runtime

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
