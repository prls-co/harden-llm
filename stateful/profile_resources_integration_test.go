//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-440

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Probe recorder delegates both methods to the actual provider effect; the
// local TLS request is retained as the assertion boundary.
type probeRecorder struct {
	delegate      hardenllm.Executor
	calls         int
	remaining     time.Duration
	model, prompt string
}

func (p *probeRecorder) Prepare(ctx context.Context, c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	p.calls++
	deadline, ok := ctx.Deadline()
	if ok {
		p.remaining = time.Until(deadline)
	}
	p.model = call.ModelID
	if len(call.Messages) == 1 {
		_ = json.Unmarshal(call.Messages[0].Content, &p.prompt)
	}
	return p.delegate.Prepare(ctx, c, k, call)
}
func (p *probeRecorder) Execute(ctx context.Context, op hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	return p.delegate.Execute(ctx, op)
}
func TestProfileResources(t *testing.T) {
	t.Parallel()
	t.Run("save probes once with exact bounded text request", func(t *testing.T) {
		p, _ := referencePool(t, true)
		var requests atomic.Int32
		s, input := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); writeFixtureOK(w) })
		recorder := &probeRecorder{delegate: s.effects.Executor}
		s.effects.Executor = recorder
		if _, err := s.SaveProfile(context.Background(), keyHashA, input); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != 1 || recorder.calls != 1 || recorder.remaining <= 9*time.Second || recorder.remaining > 10*time.Second || recorder.prompt != "Reply with OK." || recorder.model != "gpt-5.6-luna" {
			t.Fatal("probe request, deadline or attempts changed", requests.Load(), recorder.calls, recorder.remaining, recorder.prompt, recorder.model)
		}
	})
	t.Run("failed and timed-out probes save nothing and do not retry", func(t *testing.T) {
		for _, kind := range []string{"failed", "timeout"} {
			t.Run(kind, func(t *testing.T) {
				p, _ := referencePool(t, true)
				var requests atomic.Int32
				s, input := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if kind == "timeout" {
						_, _ = io.Copy(io.Discard, r.Body)
						_ = r.Body.Close()
						<-r.Context().Done()
						return
					}
					http.Error(w, "fixture rejection", 503)
				})
				ctx := context.Background()
				if kind == "timeout" {
					bounded, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
					defer cancel()
					ctx = bounded
				}
				if _, err := s.SaveProfile(ctx, keyHashA, input); err == nil {
					t.Fatal("failed probe saved candidate")
				}
				if requests.Load() != 1 || countRows(t, p, "profiles", keyHashA) != 0 {
					t.Fatal("failed probe retried or persisted", requests.Load())
				}
			})
		}
	})
	t.Run("model discovery returns changed draft without generation or saved mutation", func(t *testing.T) {
		p, _ := referencePool(t, true)
		var generates, models atomic.Int32
		s, input := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				models.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-b","object":"model"},{"id":"model-a","object":"model"}]}`))
				return
			}
			generates.Add(1)
			writeFixtureOK(w)
		})
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := json.Marshal(before)
		changed, err := s.RefreshModels(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		var def Profile
		if err = json.Unmarshal(changed.Definition, &def); err != nil {
			t.Fatal(err)
		}
		if len(def.Models) != 2 || def.Models[0].ID != "model-a" || def.LastModelRefreshAt == nil || generates.Load() != 1 || models.Load() != 1 {
			t.Fatal("refresh changed invocation semantics", generates.Load(), models.Load())
		}
		after, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		afterBytes, _ := json.Marshal(after)
		if !bytes.Equal(original, afterBytes) {
			t.Fatal("refresh changed immutable saved profile")
		}
	})
	t.Run("bundle roundtrip preserves full snapshots hidden preferences and encrypted credentials", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		entered := enteredSnapshot(t, input, "entered")
		view, err := s.SaveProfile(context.Background(), keyHashA, entered)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteProfile(context.Background(), keyHashA, "Repair Provider"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SaveProfile(context.Background(), keyHashB, input); err != nil {
			t.Fatal(err)
		}
		before, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		encodedBefore, _ := json.Marshal(before)
		exported, err := s.ExportBundle(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		var bundle ProfileBundle
		if err = json.Unmarshal(exported, &bundle); err != nil {
			t.Fatal(err)
		}
		if bundle.SchemaVersion != 4 || bundle.APIKeyHash != keyHashA || len(bundle.Profiles) != 1 || len(bundle.Credentials) != 1 || len(bundle.HiddenPresets) != 1 || bundle.Profiles[0].Profile.PresetID != "CPA Responses" {
			t.Fatal("bundle lost full catalog state")
		}
		if err = s.ReplaceBundle(context.Background(), keyHashA, exported); err != nil {
			t.Fatal(err)
		}
		after, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		encodedAfter, _ := json.Marshal(after)
		if !bytes.Equal(encodedBefore, encodedAfter) || countRows(t, p, "credentials", keyHashA) != 1 || countRows(t, p, "hidden_presets", keyHashA) != 1 || countRows(t, p, "profiles", keyHashB) != 1 {
			t.Fatal("bundle roundtrip changed snapshot or foreign scope")
		}
	})
	t.Run("scope version key and tamper rejection leave original catalog unchanged", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		input = enteredSnapshot(t, input, "entered")
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		exported, err := s.ExportBundle(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		beforeBytes, _ := json.Marshal(before)
		for _, kind := range []string{"scope", "version", "key-id", "ciphertext", "encryption-key"} {
			t.Run(kind, func(t *testing.T) {
				var candidate ProfileBundle
				if err := json.Unmarshal(exported, &candidate); err != nil {
					t.Fatal(err)
				}
				oldVault := s.config.Vault
				defer func() { s.config.Vault = oldVault }()
				switch kind {
				case "scope":
					candidate.APIKeyHash = keyHashB
				case "version":
					candidate.SchemaVersion = 3
				case "key-id":
					candidate.Credentials[0].KeyID = "other"
				case "ciphertext":
					candidate.Credentials[0].Ciphertext = "tamper"
				case "encryption-key":
					s.config.Vault, err = NewCredentialVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32)))
					if err != nil {
						t.Fatal(err)
					}
				}
				data, _ := json.Marshal(candidate)
				if err := s.ReplaceBundle(context.Background(), keyHashA, data); err == nil {
					t.Fatal("invalid bundle accepted", kind)
				}
				after, err := s.GetProfile(context.Background(), keyHashA, view.ID)
				if err != nil {
					t.Fatal(err)
				}
				afterBytes, _ := json.Marshal(after)
				if !bytes.Equal(beforeBytes, afterBytes) || countRows(t, p, "profiles", keyHashA) != 1 {
					t.Fatal("invalid bundle changed catalog")
				}
			})
		}
	})
	t.Run("candidate import probes precede transaction and failed probe changes no rows", func(t *testing.T) {
		p, _ := referencePool(t, true)
		var reject atomic.Bool
		var acquired atomic.Int32
		s, input := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) {
			acquired.Store(p.Stat().AcquiredConns())
			if reject.Load() {
				http.Error(w, "fixture rejection", 503)
				return
			}
			writeFixtureOK(w)
		})
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		data, err := s.ExportBundle(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		reject.Store(true)
		if err := s.ReplaceBundle(context.Background(), keyHashA, data); err == nil {
			t.Fatal("failing candidate probe accepted")
		}
		if acquired.Load() != 0 || countRows(t, p, "profiles", keyHashA) != 1 {
			t.Fatal("import probe held connection or changed catalog")
		}
		if _, err := s.GetProfile(context.Background(), keyHashA, view.ID); err != nil {
			t.Fatal("original catalog lost", err)
		}
	})
	t.Run("replacement transaction failure restores original catalog", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		data, err := s.ExportBundle(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		s.afterBundleDelete = func() error { return errors.New("transaction fixture failure") }
		if err := s.ReplaceBundle(context.Background(), keyHashA, data); err == nil {
			t.Fatal("injected bundle transaction failure ignored")
		}
		if countRows(t, p, "profiles", keyHashA) != 1 {
			t.Fatal("failed transaction removed old catalog")
		}
		if _, err := s.GetProfile(context.Background(), keyHashA, view.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("export contains no plaintext incoming deployment or entered secret", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		input = enteredSnapshot(t, input, "entered")
		if _, err := s.SaveProfile(context.Background(), keyHashA, input); err != nil {
			t.Fatal(err)
		}
		data, err := s.ExportBundle(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"entered-fixture-secret", "header-secret", "deployment-fixture-secret", "synthetic-hllm-key-A"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("plaintext secret exported")
			}
		}
	})
}
