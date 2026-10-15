// Command harden-llm-loadtest runs an explicitly authorized, bounded live CPA
// capacity experiment. No inference occurs unless --live is supplied.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/stateful"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	flags := flag.NewFlagSet("harden-llm-loadtest", flag.ContinueOnError)
	live := flags.Bool("live", false, "explicitly authorize CPA and paid OpenAI inference")
	directory := flags.String("output", "", "new campaign artifact directory (required)")
	duration := flags.Duration("duration", 2*time.Hour, "campaign hard inference deadline, at most 2h")
	budget := flags.Float64("api-budget", 5, "maximum reserved plus settled OpenAI USD, at most 5")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*live {
		return fmt.Errorf("live inference requires --live")
	}
	if *directory == "" || *duration <= 0 || *duration > 2*time.Hour || *budget <= 0 || *budget > 5 || math.IsNaN(*budget) || math.IsInf(*budget, 0) {
		return fmt.Errorf("require output directory, duration in (0,2h], budget in (0,5]")
	}
	cpaKey, apiKey := os.Getenv("CPA_API_KEY"), os.Getenv("OPENAI_API_KEY")
	if cpaKey == "" || apiKey == "" {
		return fmt.Errorf("CPA_API_KEY and OPENAI_API_KEY must be supplied through the environment")
	}
	if err := os.Mkdir(*directory, 0700); err != nil {
		return fmt.Errorf("new output directory: %w", err)
	}
	events, err := os.OpenFile(filepath.Join(*directory, "events.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer events.Close()
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes[:])
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{
		{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://cpa.prls.co/v1", CacheDomain: "capacity-cpa", APIKey: cpaKey},
		{ID: "openai", Provider: "openai", Protocol: "responses", BaseURL: "https://api.openai.com/v1", CacheDomain: "capacity-openai", APIKey: apiKey},
	}, DefaultConnection: "cpa", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}})
	if err != nil {
		return err
	}
	api, err := httpapi.New(httpapi.Config{APIKeys: []string{token}, Service: service})
	if err != nil {
		return err
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	diff, _ := exec.Command("git", "diff", "HEAD").Output()
	fingerprint := sha256.Sum256(diff)
	metadata := map[string]any{"sourceSHA": string(sha), "trackedDiffSHA256": hex.EncodeToString(fingerprint[:]), "model": "gpt-6-luna", "reasoning": "low", "duration": duration.String(), "apiBudgetUSD": *budget, "apiAttemptReservationUSD": livecapacity.AttemptReservation, "pricingSource": "https://developers.openai.com/api/docs/changelog#september-2026", "syntheticWorkload": true, "persistence": false, "sharedCPAConfigChanged": false}
	data, _ := json.MarshalIndent(metadata, "", "  ")
	if err = os.WriteFile(filepath.Join(*directory, "metadata.json"), data, 0600); err != nil {
		return err
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *duration)
	defer cancel()
	campaign := livecapacity.Campaign{Redactor: redaction.New(cpaKey, apiKey, token), ID: time.Now().UTC().Format("20060102T150405Z"), Directory: *directory, Started: time.Now(), Budget: *budget, Transport: livecapacity.Transport{URL: server.URL, Token: token, Client: &http.Client{Timeout: 61 * time.Second}}, Events: io.MultiWriter(events, os.Stdout)}
	runErr := campaign.Run(ctx)
	if summaryErr := campaign.Summary(); summaryErr != nil {
		return summaryErr
	}
	if err = events.Sync(); err != nil {
		return err
	}
	return runErr
}
