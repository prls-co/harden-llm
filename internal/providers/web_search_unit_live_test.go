//go:build live

package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// TestWebSearchIndexUnitLive performs exactly one request per endpoint. Run only
// after explicit approval for the two paid calls described in the parity plan.
func TestWebSearchIndexUnitLive(t *testing.T) {
	if os.Getenv("HLLM_WEB_SEARCH_PROBE_CONFIRMED") != "yes" {
		t.Skip("explicit two-call approval required")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}
	model := os.Getenv("HLLM_WEB_SEARCH_PROBE_MODEL")
	if model == "" {
		model = "gpt-5"
	}
	const prompt = "Begin your answer with exactly `🚲é🚲é — ` and then answer in one sentence with a citation: who won the most recent Tour de France?"
	client := &http.Client{Timeout: 2 * time.Minute}
	request := func(path string, body any) ([]byte, error) {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest("POST", "https://api.openai.com/v1/"+path, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		data, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s HTTP %d", path, resp.StatusCode)
		}
		return data, nil
	}
	type citation struct {
		Start int    `json:"start_index"`
		End   int    `json:"end_index"`
		URL   string `json:"url"`
	}
	var report strings.Builder
	fmt.Fprintf(&report, "# Web search index probe\n\nUTC: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	analyze := func(label, model, answer string, annotations []citation, placements []int, err error) {
		fmt.Fprintf(&report, "## %s\n\nModel: `%s`\n\n", label, model)
		if err != nil {
			fmt.Fprintf(&report, "Request failed: %s. Result: inconclusive.\n\n", err)
			return
		}
		fmt.Fprintf(&report, "Annotations: %d; stream chunk positions: %v\n\n", len(annotations), placements)
		consistent := map[string]bool{"code points": true, "UTF-16": true, "UTF-8 bytes": true}
		after := false
		for _, c := range annotations {
			u, e := url.Parse(c.URL)
			host := ""
			if e == nil {
				host = u.Hostname()
			}
			fmt.Fprintf(&report, "- Indices: %d–%d; host: `%s`\n", c.Start, c.End, host)
			prefix := strings.Index(answer, "🚲")
			if prefix >= 0 && c.Start > utf8.RuneCountInString(answer[:prefix])+4 {
				after = true
			}
			extract := func(unit string) (string, bool) {
				if c.Start < 0 || c.End < c.Start {
					return "", false
				}
				switch unit {
				case "code points":
					x := []rune(answer)
					if c.End > len(x) {
						return "", false
					}
					return string(x[c.Start:c.End]), true
				case "UTF-16":
					x := utf16.Encode([]rune(answer))
					if c.End > len(x) {
						return "", false
					}
					a, b := utf16.Decode(x[:c.Start]), utf16.Decode(x[:c.End])
					if len(a) > 0 && a[len(a)-1] == '\uFFFD' || len(b) > 0 && b[len(b)-1] == '\uFFFD' {
						return "", false
					}
					return string(utf16.Decode(x[c.Start:c.End])), true
				default:
					if c.End > len(answer) || !utf8.ValidString(answer[:c.Start]) || !utf8.ValidString(answer[:c.End]) {
						return "", false
					}
					return answer[c.Start:c.End], true
				}
			}
			for unit := range consistent {
				substring, ok := extract(unit)
				consistent[unit] = consistent[unit] && ok && host != "" && strings.Contains(substring, host)
			}
		}
		matches := []string{}
		for _, unit := range []string{"code points", "UTF-16", "UTF-8 bytes"} {
			if consistent[unit] {
				matches = append(matches, unit)
			}
		}
		if len(annotations) == 0 || !after || len(matches) != 1 {
			fmt.Fprint(&report, "\nResult: inconclusive.\n\n")
		} else {
			fmt.Fprintf(&report, "\nResult: %s.\n\n", matches[0])
		}
	}
	data, e1 := request("responses", map[string]any{"model": model, "input": prompt, "tools": []any{map[string]any{"type": "web_search"}}, "tool_choice": "required"})
	var response struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text        string     `json:"text"`
				Annotations []citation `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	var text string
	var citations []citation
	if e1 == nil {
		e1 = json.Unmarshal(data, &response)
		for _, item := range response.Output {
			if item.Type == "message" {
				for _, part := range item.Content {
					text += part.Text
					citations = append(citations, part.Annotations...)
				}
			}
		}
	}
	analyze("Responses", model, text, citations, nil, e1)
	data, e2 := request("chat/completions", map[string]any{"model": "gpt-5-search-api", "messages": []any{map[string]any{"role": "user", "content": prompt}}, "web_search_options": map[string]any{}, "stream": true})
	text = ""
	citations = nil
	var placements []int
	if e2 == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 8<<20)
		index := 0
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content     string `json:"content"`
						Annotations []struct {
							URLCitation citation `json:"url_citation"`
						} `json:"annotations"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
				e2 = err
				break
			}
			for _, choice := range chunk.Choices {
				text += choice.Delta.Content
				for _, a := range choice.Delta.Annotations {
					citations = append(citations, a.URLCitation)
					placements = append(placements, index)
				}
			}
			index++
		}
		if e2 == nil {
			e2 = scanner.Err()
		}
	}
	analyze("Chat Completions stream", "gpt-5-search-api", text, citations, placements, e2)
	dir := filepath.Join("..", "..", "plans", "evidence", "harden-llm", "web-search-parity", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.md"), []byte(report.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if e1 != nil || e2 != nil {
		t.Fatalf("probe failed; sanitized evidence written to %s", dir)
	}
	t.Logf("sanitized evidence written to %s", dir)
}
