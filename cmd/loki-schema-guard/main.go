package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/prls-co/harden-llm/internal/lokischema"
)

func main() {
	statePath := flag.String("state", "deploy/loki/schema-periods.lock.yaml", "accepted deployed schema state")
	candidatePath := flag.String("candidate", "deploy/loki/loki.yaml", "candidate Loki configuration")
	asOfText := flag.String("as-of", "", "UTC date for deterministic validation (YYYY-MM-DD)")
	flag.Parse()

	asOf := time.Now().UTC()
	if *asOfText != "" {
		parsed, err := time.Parse("2006-01-02", *asOfText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Loki schema transition validation failed: invalid --as-of: %v\n", err)
			os.Exit(2)
		}
		asOf = parsed
	}
	result, err := lokischema.ValidateFiles(*statePath, *candidatePath, asOf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Loki schema transition validation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"Loki schema transition validation passed: accepted=%d new_future=%d as_of=%s\n",
		result.AcceptedPeriods,
		len(result.NewPeriods),
		asOf.UTC().Format("2006-01-02"),
	)
}
