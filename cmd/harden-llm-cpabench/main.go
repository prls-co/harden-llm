//go:build live

// Native CPA capacity benchmark. Explicit opt-in only; never an ordinary test.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/internal/redaction"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	live := flag.Bool("live", false, "explicitly authorize native CPA inference")
	output := flag.String("output", "", "new private artifact directory")
	endpoint := flag.String("endpoint", "loopback", "loopback or public; same CPA instance")
	action := flag.String("action", "search", "search, wave or drain")
	maximum := flag.Int("maximum", 1000, "maximum search concurrency, at most 1000")
	granularity := flag.Int("granularity", 8, "stop bisection at this width")
	quiet := flag.Duration("quiet", 90*time.Second, "quiet interval before independent points >=64 and final drain")
	concurrency := flag.Int("concurrency", 64, "wave/drain concurrency")
	calls := flag.Int("calls", 1000, "drain logical items, at most 1000")
	windowBurst := flag.Int("window-burst", 0, "drain admission credits per measured quota window, zero disables")
	windowDuration := flag.Duration("window", 60*time.Second, "measured native quota window for window-burst")
	interval := flag.Duration("interval", 0, "minimum time between drain starts")
	callTimeout := flag.Duration("call-timeout", 180*time.Second, "native benchmark-only request budget, at most 5m")
	deadline := flag.Duration("duration", 2*time.Hour, "hard campaign inference deadline, at most 2h")
	batch := flag.Bool("batch", true, "run 1000-item drain after search")
	flag.Parse()
	if !*live {
		return fmt.Errorf("native CPA inference requires --live")
	}
	if *windowBurst < 0 || *windowBurst > 1000 || *windowDuration <= 0 {
		return fmt.Errorf("invalid quota window")
	}
	if *output == "" || *maximum < 1 || *maximum > 1000 || *granularity < 1 || *concurrency < 1 || *concurrency > 1000 || *calls < 1 || *calls > 1000 || *quiet < 0 || *interval < 0 || *callTimeout <= 0 || *callTimeout > 5*time.Minute || *deadline <= 0 || *deadline > 2*time.Hour {
		return fmt.Errorf("invalid bounded native benchmark parameters")
	}
	if *action != "search" && *action != "wave" && *action != "drain" {
		return fmt.Errorf("invalid action")
	}
	url := "http://127.0.0.1:8317"
	if *endpoint == "public" {
		url = "https://cpa.prls.co"
	} else if *endpoint != "loopback" {
		return fmt.Errorf("endpoint must be loopback or public")
	}
	key := os.Getenv("CPA_API_KEY")
	if key == "" {
		return fmt.Errorf("CPA_API_KEY environment value required")
	}
	if err := os.Mkdir(*output, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(*output, "events.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	diff, _ := exec.Command("git", "diff", "HEAD").Output()
	fingerprint := sha256.Sum256(diff)
	metadata := map[string]any{"windowBurst": *windowBurst, "windowMs": windowDuration.Milliseconds(), "sourceSHA": string(sha), "trackedDiffSHA256": hex.EncodeToString(fingerprint[:]), "endpoint": url, "nativeCPAStreaming": true, "model": "gpt-6-luna", "reasoning": "low", "action": *action, "maximum": *maximum, "granularity": *granularity, "quietMs": quiet.Milliseconds(), "concurrency": *concurrency, "calls": *calls, "intervalMs": interval.Milliseconds(), "callTimeoutMs": callTimeout.Milliseconds(), "durationMs": deadline.Milliseconds(), "officialAPIEnabled": false, "sharedCPAConfigurationChanged": false, "HLLMExecutionCoreUsed": false}
	raw, _ := json.MarshalIndent(metadata, "", "  ")
	if err = os.WriteFile(filepath.Join(*output, "metadata.json"), raw, 0600); err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 1000, MaxIdleConnsPerHost: 1000, IdleConnTimeout: 2 * time.Minute, ResponseHeaderTimeout: *callTimeout, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	caller := livecapacity.NativeCaller{URL: url, Key: key, Client: &http.Client{Transport: transport}, Timeout: *callTimeout}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *deadline)
	defer cancel()
	b := livecapacity.NativeBench{WindowBurst: *windowBurst, WindowDuration: *windowDuration, Directory: *output, Events: io.MultiWriter(file, os.Stdout), Started: time.Now(), Call: caller.Call, Redactor: redaction.New(key)}
	switch *action {
	case "search":
		lo, hi, err := b.Search(ctx, *maximum, *granularity, *quiet)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(map[string]any{"lower": lo, "upper": hi, "criterion": "no explicit throttle; unknown outliers remain counted separately"}, "", "  ")
		if err = os.WriteFile(filepath.Join(*output, "bracket.json"), raw, 0600); err != nil {
			return err
		}
		if !*batch {
			return file.Sync()
		}
		if lo < 1 {
			return fmt.Errorf("no usable baseline")
		}
		if err = b.Wait(ctx, "quiet-before-1000-drain", *quiet); err != nil {
			return err
		}
		p, err := b.Point(ctx, "drain-1000", lo, 1000, 0, true)
		if err != nil {
			return err
		}
		if p.Completed != 1000 {
			return fmt.Errorf("1000-item drain incomplete; unknown outcomes retained without replay")
		}
	case "wave":
		p, err := b.Point(ctx, "wave", *concurrency, *concurrency, 0, false)
		if err != nil {
			return err
		}
		if p.Completed != p.Accepted {
			return fmt.Errorf("wave includes incomplete/rejected calls")
		}
	case "drain":
		p, err := b.Point(ctx, "drain", *concurrency, *calls, *interval, true)
		if err != nil {
			return err
		}
		if p.Completed != p.Accepted {
			return fmt.Errorf("drain includes incomplete calls")
		}
	}
	return file.Sync()
}
