// reference-gateway is a test-only process. It never loads deployment settings,
// production keys, a public upstream, or an in-memory persistence substitute.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/testfixture/reference"
	"github.com/prls-co/harden-llm/stateful"
)

const queryProfileID = "fixture-query-profile"
const utilityProfileID = "fixture-utility-profile"

var ownedSchemaPattern = regexp.MustCompile(`^hllm_fixture_[a-f0-9]{32}$`)

type config struct {
	key      string
	schema   string
	pool     *pgxpool.Config
	scenario *reference.Scenario
}

type readiness struct {
	Origin           string `json:"origin"`
	QueryProfileID   string `json:"queryProfileId"`
	UtilityProfileID string `json:"utilityProfileId"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readConfig(getenv func(string) string) (config, error) {
	var result config
	key := getenv("PRLS_FIXTURE_CLIENT_KEY")
	dsn := getenv("PRLS_FIXTURE_DATABASE_URL")
	scenarioPath := getenv("PRLS_FIXTURE_SCENARIO_FILE")
	if !auth.ValidToken(key) || dsn == "" || !filepath.IsAbs(scenarioPath) {
		return result, errors.New("fixture: three explicit valid fixture inputs are required")
	}
	u, err := url.Parse(dsn)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() != "127.0.0.1" || u.Fragment != "" || u.Path == "/" || strings.Count(u.Path, "/") != 1 {
		return result, errors.New("fixture: an explicit loopback PostgreSQL URL is required")
	}
	password, hasPassword := u.User.Password()
	port, portErr := strconv.Atoi(u.Port())
	query, queryErr := url.ParseQuery(u.RawQuery)
	schema := query.Get("search_path")
	if !hasPassword || password == "" || portErr != nil || port < 1 || port > 65535 || queryErr != nil || len(query) != 2 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" || len(query["search_path"]) != 1 || !ownedSchemaPattern.MatchString(schema) {
		return result, errors.New("fixture: an explicit credential and single owned SQL schema are required")
	}
	pool, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return result, errors.New("fixture: invalid explicit SQL configuration")
	}
	pool.MaxConns, pool.MinConns = 4, 0
	pool.ConnConfig.ConnectTimeout = 5 * time.Second
	pool.ConnConfig.Fallbacks = nil
	pool.ConnConfig.RuntimeParams = map[string]string{"search_path": schema, "application_name": schema, "statement_timeout": "5000", "lock_timeout": "5000"}
	file, err := os.Open(scenarioPath)
	if err != nil {
		return result, errors.New("fixture: cannot open the explicit scenario file")
	}
	defer file.Close()
	scenario, err := reference.Decode(file)
	if err != nil {
		return result, err
	}
	return config{key: key, schema: schema, pool: pool, scenario: scenario}, nil
}

// fixtureProfiles changes only identity labels and the synthetic model/endpoint.
// All canonical capabilities, validation and recovery setup stay at their owner.
func fixtureProfiles(origin string) (stateful.Catalog, error) {
	canonical, err := stateful.DefaultCatalog()
	if err != nil {
		return nil, errors.New("fixture: canonical profile catalog is unavailable")
	}
	result := stateful.Catalog{}
	for id, model := range map[string]string{queryProfileID: "fixture-query-model", utilityProfileID: "fixture-utility-model"} {
		preset := canonical["CPA gpt-6.1-sol"]
		var definition, inputs map[string]any
		if json.Unmarshal(preset.Definition, &definition) != nil || json.Unmarshal(preset.Inputs, &inputs) != nil {
			return nil, errors.New("fixture: invalid canonical profile template")
		}
		definition["baseUrl"], definition["modelId"], inputs["modelId"] = origin+"/v1", model, model
		preset.Name = id
		preset.Definition, _ = json.Marshal(definition)
		preset.Inputs, _ = json.Marshal(inputs)
		result[id] = preset
	}
	return result, nil
}

func startProvider(ctx context.Context, handler http.Handler) *httptest.Server {
	provider := httptest.NewUnstartedServer(handler)
	provider.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	provider.Config.ErrorLog = log.New(io.Discard, "", 0)
	provider.StartTLS()
	return provider
}

func fixtureClient(provider *httptest.Server, preset stateful.Preset) (stateful.ClientOptions, error) {
	var definition struct {
		Provider string `json:"provider"`
		Protocol string `json:"apiInferenceType"`
	}
	if json.Unmarshal(preset.Definition, &definition) != nil || definition.Provider == "" || definition.Protocol == "" || preset.Binding.DeploymentUpstreamID == "" {
		return stateful.ClientOptions{}, errors.New("fixture: invalid canonical upstream metadata")
	}
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	ownedAddress := provider.Listener.Addr().String()
	return stateful.ClientOptions{
		Connections:       []hardenllm.Connection{{ID: preset.Binding.DeploymentUpstreamID, Provider: definition.Provider, Protocol: definition.Protocol, BaseURL: provider.URL + "/v1", APIKey: "synthetic-fixture-provider-key"}},
		DefaultConnection: preset.Binding.DeploymentUpstreamID,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		EndpointPolicy: hardenllm.EndpointPolicy{
			AllowedHosts: []string{"127.0.0.1"}, PrivateAllowedHosts: []string{"127.0.0.1"},
			TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != ownedAddress {
					return nil, errors.New("fixture: provider socket is outside the owned endpoint")
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			},
		},
	}, nil
}

func run(ctx context.Context, getenv func(string) string, ready io.Writer) (resultErr error) {
	config, err := readConfig(getenv)
	if err != nil {
		return err
	}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	startup, stopStartup := context.WithTimeout(lifetime, 30*time.Second)
	defer stopStartup()
	pool, err := pgxpool.NewWithConfig(startup, config.pool)
	if err != nil {
		return errors.New("fixture: cannot create the scoped SQL pool")
	}
	defer pool.Close()
	var currentSchema string
	var owned bool
	if pool.QueryRow(startup, `SELECT current_schema(), nspowner=current_user::regrole FROM pg_namespace WHERE nspname=$1`, config.schema).Scan(&currentSchema, &owned) != nil || currentSchema != config.schema || !owned {
		return errors.New("fixture: the explicit SQL schema must already exist and belong to its credential")
	}
	if stateful.ApplySchema(startup, pool) != nil {
		return errors.New("fixture: owner SQL schema initialization failed")
	}
	provider := startProvider(lifetime, config.scenario.Provider())
	defer func() {
		cancel()
		provider.CloseClientConnections()
		provider.Close()
	}()
	profiles, err := fixtureProfiles(provider.URL)
	if err != nil {
		return err
	}
	clientOptions, err := fixtureClient(provider, profiles[queryProfileID])
	if err != nil {
		return err
	}
	service, err := stateful.NewService(stateful.Config{Pool: pool, Presets: profiles, Client: clientOptions, History: stateful.HistoryConfig{Enabled: true}})
	if err != nil {
		return errors.New("fixture: actual stateful composition failed")
	}
	api, err := httpapi.New(httpapi.Config{APIKeys: []string{config.key}, Service: service, Readiness: []httpapi.ReadinessCheck{pool.Ping}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		return errors.New("fixture: actual gateway composition failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("fixture: cannot bind the owned gateway listener")
	}
	server := &http.Server{Handler: config.scenario.Gateway(config.key, api.Handler()), ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return lifetime }, ErrorLog: log.New(io.Discard, "", 0)}
	served := make(chan struct{})
	go func() { defer close(served); _ = server.Serve(listener) }()
	defer func() {
		cancel()
		cleanup, stopCleanup := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCleanup()
		if server.Shutdown(cleanup) != nil {
			_ = server.Close()
			resultErr = errors.New("fixture: owned gateway required forced shutdown")
		}
		<-served
	}()
	origin := "http://" + listener.Addr().String()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(startup, http.MethodGet, origin+"/readyz", nil)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("fixture: owned gateway readiness failed")
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("fixture: actual owner readiness rejected startup")
	}
	if json.NewEncoder(ready).Encode(readiness{Origin: origin, QueryProfileID: queryProfileID, UtilityProfileID: utilityProfileID}) != nil {
		return errors.New("fixture: cannot publish readiness")
	}
	select {
	case <-lifetime.Done():
		return nil
	case <-served:
		return errors.New("fixture: owned gateway stopped before cancellation")
	}
}
