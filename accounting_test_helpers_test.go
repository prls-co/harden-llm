package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-058

import (
	"github.com/prls-co/harden-llm/internal/accounting"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

func testLedger(input, cacheRead, cacheCreation, output, reasoning int64, cost accounting.Cost) coreruntime.Ledger {
	usage, err := accounting.CompleteUsage(input, cacheRead, cacheCreation, output, reasoning)
	if err != nil {
		panic(err)
	}
	return coreruntime.Ledger{Usage: usage, Cost: cost}
}
