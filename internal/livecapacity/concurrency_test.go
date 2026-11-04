package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-470

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrencyWaveActuallyOverlapsAndNeverOverflows(t *testing.T) {
	const concurrency = 8
	var active, peak atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n == concurrency {
			close(release)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
		// Known rejection must stop this wave with no retries or API overflow.
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":"rate_limit"}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := Campaign{ID: "fixture", Directory: t.TempDir(), Started: time.Now(), Events: io.Discard, Transport: Transport{URL: server.URL, Client: server.Client()}}
	s, err := c.RunPhase(ctx, "wave", concurrency, RouteConfig{Concurrency: concurrency}, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() != concurrency || s.PeakAPI != 0 || len(c.Measurements) != concurrency {
		t.Fatalf("wave did not overlap or overflowed: peak=%d scheduler=%+v measurements=%d", peak.Load(), s, len(c.Measurements))
	}
	for _, m := range c.Measurements {
		if m.Route != "cpa" || m.Attempt != 1 {
			t.Fatalf("replayed/overflowed: %+v", m)
		}
	}
}

func TestConcurrencyInvalidBoundsNeverDispatch(t *testing.T) {
	c := Campaign{}
	for _, bounds := range [][3]int{{0, 64, 3}, {65, 64, 3}, {1, 129, 1}, {1, 64, 0}} {
		if err := c.RunConcurrency(context.Background(), bounds[0], bounds[1], bounds[2]); err == nil {
			t.Fatal("invalid bounds accepted", bounds)
		}
	}
}
