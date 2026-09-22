package capacity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxScenarioCatalogBytes = 1 << 20

type ScenarioSpec struct {
	ID                 string `json:"id"`
	Seed               int64  `json:"seed"`
	OfferedRPS         int    `json:"offeredRps"`
	WarmupSeconds      int    `json:"warmupSeconds"`
	MeasurementSeconds int    `json:"measurementSeconds"`
	DrainSeconds       int    `json:"drainSeconds"`
	MaxRequests        int    `json:"maxRequests"`
	MaxInflight        int    `json:"maxInflight"`
	ProviderScript     string `json:"providerScript"`
	ProviderDelayMS    int    `json:"providerDelayMs"`
	ExpectedOutcome    string `json:"expectedOutcome"`
	CacheMode          string `json:"cacheMode"`
	RecoveryPolicy     string `json:"recoveryPolicy"`
	TelemetryMode      string `json:"telemetryMode"`
}

type ScenarioCatalog struct {
	SchemaVersion int                       `json:"schemaVersion"`
	CaseSets      map[string][]ScenarioSpec `json:"caseSets"`
}

func ParseScenarioCatalog(data []byte) (ScenarioCatalog, error) {
	if len(data) == 0 || len(data) > maxScenarioCatalogBytes {
		return ScenarioCatalog{}, fmt.Errorf("scenario catalog must contain 1 to %d bytes", maxScenarioCatalogBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog ScenarioCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return ScenarioCatalog{}, fmt.Errorf("decode scenario catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ScenarioCatalog{}, errors.New("scenario catalog must contain one JSON value")
		}
		return ScenarioCatalog{}, fmt.Errorf("scenario catalog trailing data: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return ScenarioCatalog{}, err
	}
	return catalog, nil
}

func (catalog ScenarioCatalog) Validate() error {
	if catalog.SchemaVersion != 1 {
		return fmt.Errorf("scenario catalog schemaVersion must be 1, got %d", catalog.SchemaVersion)
	}
	if len(catalog.CaseSets) == 0 {
		return errors.New("scenario catalog must define at least one case set")
	}
	for caseSet, scenarios := range catalog.CaseSets {
		if !validCaseSet(caseSet) {
			return fmt.Errorf("unsupported capacity case set %q", caseSet)
		}
		if len(scenarios) == 0 {
			return fmt.Errorf("capacity case set %q is empty", caseSet)
		}
		seen := make(map[string]struct{}, len(scenarios))
		for _, scenario := range scenarios {
			if err := scenario.Validate(); err != nil {
				return fmt.Errorf("scenario %q in %s: %w", scenario.ID, caseSet, err)
			}
			if _, exists := seen[scenario.ID]; exists {
				return fmt.Errorf("duplicate scenario ID %q in case set %q", scenario.ID, caseSet)
			}
			seen[scenario.ID] = struct{}{}
		}
	}
	return nil
}

func (scenario ScenarioSpec) Validate() error {
	if strings.TrimSpace(scenario.ID) == "" || len(scenario.ID) > 80 {
		return errors.New("id must contain 1 to 80 non-whitespace characters")
	}
	for _, char := range scenario.ID {
		if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' {
			return errors.New("id may contain lowercase letters, digits, and hyphens only")
		}
	}
	if scenario.Seed <= 0 {
		return errors.New("seed must be positive")
	}
	if scenario.WarmupSeconds < 0 || scenario.DrainSeconds < 0 {
		return errors.New("warmup and drain seconds cannot be negative")
	}
	if scenario.WarmupSeconds > 600 || scenario.DrainSeconds > 120 {
		return errors.New("warmupSeconds must be at most 600 and drainSeconds at most 120")
	}
	if scenario.MeasurementSeconds <= 0 {
		return errors.New("measurementSeconds must be positive")
	}
	if scenario.MeasurementSeconds > 900 {
		return errors.New("measurementSeconds must be at most 900")
	}
	if scenario.ProviderDelayMS < 0 || scenario.ProviderDelayMS > 10_000 {
		return errors.New("providerDelayMs must be between zero and 10000")
	}
	driverScenario := scenario.DriverScenario()
	if err := validateScenario(driverScenario); err != nil {
		return err
	}
	if _, err := BuildArrivalSchedule(driverScenario); err != nil {
		return err
	}
	if !oneOf(scenario.ProviderScript, "success", "rate-limit-once", "rate-limit", "malformed-json", "truncated-stream", "slow-success") {
		return fmt.Errorf("unsupported providerScript %q", scenario.ProviderScript)
	}
	if !oneOf(scenario.ExpectedOutcome, "succeeded", "failed", "rejected", "canceled", "unfinished") {
		return fmt.Errorf("unsupported expectedOutcome %q", scenario.ExpectedOutcome)
	}
	if (scenario.ProviderScript == "slow-success") != (scenario.ProviderDelayMS > 0) {
		return errors.New("providerDelayMs must be positive only for slow-success")
	}
	if !oneOf(scenario.CacheMode, "off", "hit", "miss", "mixed") {
		return fmt.Errorf("unsupported cacheMode %q", scenario.CacheMode)
	}
	if !oneOf(scenario.RecoveryPolicy, "off", "default", "json-repair", "retry") {
		return fmt.Errorf("unsupported recoveryPolicy %q", scenario.RecoveryPolicy)
	}
	if !oneOf(scenario.TelemetryMode, "disabled", "local-sink", "blocked-exporter") {
		return fmt.Errorf("unsupported telemetryMode %q", scenario.TelemetryMode)
	}
	return nil
}

func (scenario ScenarioSpec) DriverScenario() Scenario {
	return Scenario{
		OfferedRPS:     scenario.OfferedRPS,
		Warmup:         time.Duration(scenario.WarmupSeconds) * time.Second,
		Measurement:    time.Duration(scenario.MeasurementSeconds) * time.Second,
		Drain:          time.Duration(scenario.DrainSeconds) * time.Second,
		MaxRequests:    scenario.MaxRequests,
		MaxInflight:    scenario.MaxInflight,
		RequireJSON:    true,
		CacheMode:      scenario.CacheMode,
		RecoveryPolicy: scenario.RecoveryPolicy,
		Origin: RequestOrigin{
			Client: "harden-llm-capacity-test", Component: "capacity-baseline",
			OperationID: scenario.ID, TestID: "TEST-277",
		},
	}
}

func validCaseSet(value string) bool {
	return oneOf(value, "correctness", "exploration", "holdout")
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
