package gateway

import hardenllm "github.com/prls-co/harden-llm"

// NewStaticClient constructs the one immutable configured engine used by the
// HTTP proxy. Product identity and history never enter runtime construction.
func NewStaticClient(options hardenllm.Options) (*hardenllm.Client, error) {
	return hardenllm.New(options)
}
