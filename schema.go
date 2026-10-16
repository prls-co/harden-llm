package hardenllm

import (
	"encoding/json"
	"github.com/prls-co/harden-llm/internal/schema"
)

// NormalizeSchema validates and detaches the core's structured-output contract
// without invoking a provider. Durable callers use it before accepting work.
func NormalizeSchema(value json.RawMessage) (json.RawMessage, error) { return schema.Normalize(value) }
