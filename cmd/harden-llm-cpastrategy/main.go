//go:build live

// Explicit CPA-only competing-client qualification, never a deployment smoke.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/stateful/batch"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	live := flag.Bool("live", false, "authorize CPA-only inference")
	out := flag.String("output", "", "fresh absolute private evidence directory")
	mode := flag.String("policy", "windowed", "windowed or continuous")
	calls := flag.Int("calls", 1000, "foreground logical requests, 1..10000")
	competing := flag.Bool("competing", true, "offer an independent 200-request competing workload")
	shared := flag.Bool("shared", false, "coordinate both clients through one controller")
	mixed := flag.Bool("mixed", false, "10 percent of foreground requests require bounded 512-word output")
	longEvery := flag.Int("long-every", 0, "bounded 512-word output every N foreground items; 1 means all, 0 disables")
	contextWords := flag.Int("context-words", 0, "distinct synthetic input words per request, 0..2048; measured tokens remain authoritative")
	trafficStop := flag.Bool("traffic-stop", false, "distinct bounded offered-background experiment; quota refusals are terminal background outcomes")
	budget := flag.Int64("token-budget", 8000000, "reported input+output allowance including active/unknown reserves, at most 10M")
	quiet := flag.Duration("quiet", 90*time.Second, "pre-run quiet observation, not an assertion that account quota reset")
	deadline := flag.Duration("deadline", 30*time.Minute, "hard experiment deadline, at most 2h")
	endpoint := flag.String("endpoint", "public", "public or loopback CPA")
	flag.Parse()
	if !*live || os.Getenv("CPA_API_KEY") == "" {
		return fmt.Errorf("--live and protected CPA_API_KEY required")
	}
	if !filepath.IsAbs(*out) || *calls < 1 || *calls > 10000 || *budget < 1 || *budget > 10000000 || *quiet < 0 || *quiet > 5*time.Minute || *deadline < time.Second || *deadline > 2*time.Hour || (*shared && !*competing) {
		return fmt.Errorf("invalid bounded experiment settings")
	}
	if *contextWords < 0 || *contextWords > 2048 || *longEvery < 0 || *longEvery > 1000 || (*mixed && *longEvery != 0) || (*trafficStop && (!*competing || *shared)) {
		return fmt.Errorf("invalid output-load or offered-traffic settings")
	}
	if *mixed {
		*longEvery = 10
	}
	p := batch.DefaultPolicy()
	if *mode == "continuous" {
		p.WindowRequests = 0
	} else if *mode != "windowed" {
		return fmt.Errorf("invalid policy")
	}
	url := "https://cpa.prls.co"
	if *endpoint == "loopback" {
		url = "http://127.0.0.1:8317"
	} else if *endpoint != "public" {
		return fmt.Errorf("invalid CPA endpoint")
	}
	if err := os.Mkdir(*out, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(*out, "events.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	writeJSON := func(name string, v any) error {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(*out, name)
		if err = os.WriteFile(path+".tmp", append(raw, '\n'), 0600); err != nil {
			return err
		}
		return os.Rename(path+".tmp", path)
	}
	sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	diff, _ := exec.Command("git", "diff", "--binary", "HEAD").Output()
	hash := sha256.Sum256(diff)
	binary, _ := os.Executable()
	binaryData, _ := os.ReadFile(binary)
	binaryHash := sha256.Sum256(binaryData)
	metadata := map[string]any{"sourceSHA": strings.TrimSpace(string(sha)), "sourceDirty": len(diff) > 0, "sourceDiffSHA256": hex.EncodeToString(hash[:]), "binarySHA256": hex.EncodeToString(binaryHash[:]), "model": "gpt-6-luna", "reasoning": "low", "endpoint": url, "policy": *mode, "foregroundCalls": *calls, "competing": *competing, "sharedController": *shared, "mixedBoundedText": *mixed, "tokenBudget": *budget, "nativeInternalAttempts": nil, "officialAPIUsed": false, "sharedCPAConfigurationChanged": false, "TemporalUsed": false, "startedAt": time.Now().UTC()}
	metadata["longEvery"] = *longEvery
	metadata["contextWords"] = *contextWords
	metadata["offeredBackgroundTrafficStops"] = *trafficStop
	if *trafficStop {
		metadata["minimumForegroundItemsAdmittedAfterBackgroundFinished"] = min(100, *calls)
		metadata["backgroundExplicitQuotaRefusalsAreTerminalLoadOutcomes"] = true
	}
	if err = writeJSON("metadata.json", metadata); err != nil {
		return err
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *deadline)
	defer cancel()
	quietUntil := time.Now().Add(*quiet)
	for time.Now().Before(quietUntil) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(5*time.Second, time.Until(quietUntil))):
			fmt.Printf("{\"phase\":\"quiet\",\"remainingSeconds\":%.0f}\n", max(0, time.Until(quietUntil).Seconds()))
		}
	}
	actors, items := livecapacity.StrategyWorkload(p, *calls, *competing, *shared)
	if *trafficStop {
		actors, items = livecapacity.StrategyOfferedTraffic(p, *calls)
	}
	e, err := livecapacity.NewStrategyEngine(time.Now(), actors, items, *budget)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 1000, MaxIdleConnsPerHost: 1000, MaxConnsPerHost: 1000, IdleConnTimeout: 2 * time.Minute, ResponseHeaderTimeout: 180 * time.Second, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	caller := livecapacity.NativeCaller{URL: url, Key: os.Getenv("CPA_API_KEY"), Client: client, Timeout: 180 * time.Second, ContextWords: *contextWords}
	call := func(ctx context.Context, prefix string, id, attempt int) livecapacity.NativeMeasurement {
		c := caller
		c.BoundedText = *longEvery > 0 && id < *calls && id%*longEvery == 0
		return c.Call(ctx, prefix, id, attempt)
	}
	progress := func(v livecapacity.StrategySnapshot) error {
		if err := writeJSON("status.json", v); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"phase": "running", "elapsedMs": v.ElapsedMs, "actors": v.Actors, "stopReason": v.StopReason, "reportedTokens": v.ReportedTokens, "unknownReserve": v.UnknownTokenReserve})
	}
	err = livecapacity.RunStrategy(ctx, e, "strategy-"+uuid.NewString(), call, file, progress)
	s := e.Snapshot(time.Now())
	status := "passed"
	if err != nil {
		status = "failed"
	}
	for i, a := range s.Actors {
		qualified := a.Completed
		if i > 0 && actors[i].RefusalIsTerminal {
			qualified += a.TerminalRefusals
		}
		if qualified != a.Offered || a.Unknown > 0 || a.Failed > 0 || a.Policy > 0 {
			status = "failed"
		}
	}
	coverageFailure := ""
	if *trafficStop && s.ForegroundAdmittedAfterBackgroundFinished < min(100, *calls) {
		status = "failed"
		coverageFailure = "insufficient_foreground_dispatch_after_background_finished"
	}
	summary := map[string]any{"status": status, "measurement": s, "metadata": metadata, "finishedAt": time.Now().UTC(), "reportedUsageIsNotNativeInternalUsage": true}
	summary["coverageFailure"] = coverageFailure
	if writeErr := writeJSON("summary.json", summary); writeErr != nil {
		return writeErr
	}
	if syncErr := file.Sync(); syncErr != nil {
		return syncErr
	}
	if err != nil {
		return err
	}
	if status != "passed" {
		return fmt.Errorf("distinct result qualification incomplete; inspect bounded summary")
	}
	return nil
}
