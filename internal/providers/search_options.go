package providers

import (
	"errors"
	"strings"
)

// The first-class toggle owns hosted search tools. Preserve unrelated tools;
// discard advanced search overrides so OFF cannot accidentally perform search.
func manageSearchOptions(options map[string]any) error {
	var tools []any
	switch value := options["tools"].(type) {
	case nil:
	case []any:
		tools = value
	case []map[string]any:
		for _, tool := range value {
			tools = append(tools, tool)
		}
	default:
		return errors.New("providers: tools must be an array of objects")
	}
	kept := make([]any, 0, len(tools))
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			return errors.New("providers: tools must be an array of objects")
		}
		kind, _ := tool["type"].(string)
		_, google := tool["google_search"]
		_, googleCamel := tool["googleSearch"]
		_, googleLegacy := tool["google_search_retrieval"]
		if strings.HasPrefix(kind, "web_search") || google || googleCamel || googleLegacy {
			continue
		}
		kept = append(kept, tool)
	}
	if len(kept) == 0 {
		delete(options, "tools")
		delete(options, "tool_choice")
	} else {
		options["tools"] = kept
	}
	if choice, ok := options["tool_choice"].(map[string]any); ok && (strings.HasPrefix(stringValue(choice["type"]), "web_search") || choice["name"] == "web_search") {
		delete(options, "tool_choice")
	}
	delete(options, "web_search_options")
	return nil
}
