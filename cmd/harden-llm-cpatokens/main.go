//go:build live

// A bounded, explicit live experiment. It never changes CPA configuration.
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
	"syscall"
	"time"

	"github.com/prls-co/harden-llm/internal/livecapacity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	live := flag.Bool("live", false, "authorize at most 66 separate CPA caller requests")
	dir := flag.String("output", "", "new private evidence directory")
	flag.Parse()
	if !*live || *dir == "" || os.Getenv("CPA_API_KEY") == "" {
		return fmt.Errorf("requires --live, --output and protected CPA_API_KEY")
	}
	if err := os.Mkdir(*dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(*dir, "events.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	binary, _ := os.Executable()
	bytes, _ := os.ReadFile(binary)
	sum := sha256.Sum256(bytes)
	meta, _ := json.MarshalIndent(map[string]any{"sourceSHA": string(sha), "binarySHA256": hex.EncodeToString(sum[:]), "model": "gpt-6-luna", "reasoning": "low", "endpoint": "https://cpa.prls.co", "maximumCallerRequests": 66, "maximumDurationSeconds": 900, "generatedTokenStopAdmission": 50000, "order": []string{"short", "long", "long", "short"}, "quietSeconds": 90, "requestedOutputCapNotForwardedByCodexTranslator": true, "configurationChanged": false}, "", "  ")
	if err = os.WriteFile(filepath.Join(*dir, "metadata.json"), meta, 0600); err != nil {
		return err
	}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 32, MaxIdleConnsPerHost: 32, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 180 * time.Second}
	defer tr.CloseIdleConnections()
	caller := livecapacity.NativeCaller{URL: "https://cpa.prls.co", Key: os.Getenv("CPA_API_KEY"), Client: &http.Client{Transport: tr}, Timeout: 180 * time.Second}
	prefix := fmt.Sprintf("tokens-%d", time.Now().UnixNano())
	enc := json.NewEncoder(f)
	console := json.NewEncoder(os.Stdout)
	var measurements []livecapacity.NativeMeasurement
	var tokens int64
	lastProgress := time.Now().UTC()
	emit := func(v any) { _ = enc.Encode(v); _ = console.Encode(v); _ = f.Sync() }
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	consume := func(m livecapacity.NativeMeasurement) {
		measurements = append(measurements, m)
		tokens += m.OutputTokens
		lastProgress = time.Now().UTC()
		emit(map[string]any{"type": "measurement", "measurement": m})
	}
	wait := func(d time.Duration) error {
		until := time.Now().Add(d)
		timer := time.NewTimer(d)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			case <-heartbeat.C:
				emit(map[string]any{"type": "heartbeat", "phase": "quiet", "completed": len(measurements), "generatedTokens": tokens, "nextAttemptAt": until.UTC(), "lastProgressAt": lastProgress})
			}
		}
	}
	for i, n := range []int{4, 512} {
		caller.OutputItems = n
		m := caller.Call(ctx, prefix, i, 1)
		consume(m)
		if m.Kind != "completed" {
			return fmt.Errorf("calibration failed; evidence retained without replay")
		}
	}
	for run, n := range []int{4, 512, 512, 4} {
		if err = wait(90 * time.Second); err != nil {
			return err
		}
		caller.OutputItems = n
		emit(map[string]any{"type": "run_start", "run": run, "expectedItems": n, "at": time.Now().UTC()})
		results := make(chan livecapacity.NativeMeasurement, 16)
		launched, done := 0, 0
		tick := time.NewTicker(100 * time.Millisecond)
		for done < 16 {
			select {
			case <-ctx.Done():
				tick.Stop()
				return ctx.Err()
			case <-tick.C:
				if launched < 16 {
					if tokens >= 50000 {
						tick.Stop()
						return fmt.Errorf("token admission budget reached; evidence retained")
					}
					id := 2 + run*16 + launched
					launched++
					go func(c livecapacity.NativeCaller, id int) { results <- c.Call(ctx, prefix, id, 1) }(caller, id)
				}
			case m := <-results:
				consume(m)
				done++
			case <-heartbeat.C:
				emit(map[string]any{"type": "heartbeat", "phase": "running", "run": run, "launched": launched, "finished": done, "completed": len(measurements), "lastProgressAt": lastProgress})
			}
		}
		tick.Stop()
	}
	raw, _ := json.MarshalIndent(map[string]any{"measurements": measurements, "generatedTokens": tokens, "callerRequests": len(measurements)}, "", "  ")
	return os.WriteFile(filepath.Join(*dir, "summary.json"), raw, 0600)
}
