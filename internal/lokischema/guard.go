package lokischema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const StateSchemaVersion = "harden_loki_schema_state.v1"

type IndexPeriod struct {
	Prefix string `yaml:"prefix" json:"prefix"`
	Period string `yaml:"period" json:"period"`
}

type Period struct {
	From        string      `yaml:"from" json:"from"`
	Store       string      `yaml:"store" json:"store"`
	ObjectStore string      `yaml:"object_store" json:"object_store"`
	Schema      string      `yaml:"schema" json:"schema"`
	Index       IndexPeriod `yaml:"index" json:"index"`
}

type candidateConfig struct {
	SchemaConfig struct {
		Configs []Period `yaml:"configs"`
	} `yaml:"schema_config"`
}

type StatePeriod struct {
	From        string `yaml:"from"`
	Fingerprint string `yaml:"fingerprint_sha256"`
}

type State struct {
	SchemaVersion string        `yaml:"schema_version"`
	RecordedAt    string        `yaml:"recorded_at"`
	Periods       []StatePeriod `yaml:"periods"`
}

type Result struct {
	AcceptedPeriods int
	NewPeriods      []string
}

func Fingerprint(period Period) (string, error) {
	encoded, err := json.Marshal(period)
	if err != nil {
		return "", fmt.Errorf("encode Loki schema period: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateFiles(statePath, candidatePath string, asOf time.Time) (Result, error) {
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		return Result{}, fmt.Errorf("read deployed schema state: %w", err)
	}
	candidateData, err := os.ReadFile(candidatePath)
	if err != nil {
		return Result{}, fmt.Errorf("read candidate Loki config: %w", err)
	}
	return Validate(stateData, candidateData, asOf)
}

func Validate(stateData, candidateData []byte, asOf time.Time) (Result, error) {
	var state State
	if err := yaml.Unmarshal(stateData, &state); err != nil {
		return Result{}, fmt.Errorf("decode deployed schema state: %w", err)
	}
	if state.SchemaVersion != StateSchemaVersion {
		return Result{}, fmt.Errorf("deployed schema state version is %q", state.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, state.RecordedAt); err != nil {
		return Result{}, fmt.Errorf("deployed schema state recorded_at is invalid: %w", err)
	}

	var candidate candidateConfig
	if err := yaml.Unmarshal(candidateData, &candidate); err != nil {
		return Result{}, fmt.Errorf("decode candidate Loki config: %w", err)
	}
	periods := candidate.SchemaConfig.Configs
	if len(periods) == 0 {
		return Result{}, errors.New("candidate Loki config has no schema periods")
	}

	candidateByDate := make(map[string]Period, len(periods))
	previousDate := ""
	for index, period := range periods {
		date, err := parsePeriod(period)
		if err != nil {
			return Result{}, fmt.Errorf("candidate period %d: %w", index, err)
		}
		if previousDate != "" && period.From <= previousDate {
			return Result{}, errors.New("candidate schema periods must be unique and ascending")
		}
		previousDate = period.From
		candidateByDate[period.From] = period
		_ = date
	}

	accepted := make(map[string]string, len(state.Periods))
	previousDate = ""
	for index, period := range state.Periods {
		if _, err := time.Parse("2006-01-02", period.From); err != nil {
			return Result{}, fmt.Errorf("deployed period %d has invalid from date: %w", index, err)
		}
		if previousDate != "" && period.From <= previousDate {
			return Result{}, errors.New("deployed schema periods must be unique and ascending")
		}
		if len(period.Fingerprint) != 64 {
			return Result{}, fmt.Errorf("deployed period %s has invalid fingerprint", period.From)
		}
		if _, err := hex.DecodeString(period.Fingerprint); err != nil {
			return Result{}, fmt.Errorf("deployed period %s has invalid fingerprint", period.From)
		}
		previousDate = period.From
		accepted[period.From] = period.Fingerprint
	}
	if len(accepted) == 0 {
		return Result{}, errors.New("deployed schema state has no accepted periods")
	}

	for from, expected := range accepted {
		period, exists := candidateByDate[from]
		if !exists {
			return Result{}, fmt.Errorf("candidate removes deployed schema period %s", from)
		}
		actual, err := Fingerprint(period)
		if err != nil {
			return Result{}, err
		}
		if actual != expected {
			return Result{}, fmt.Errorf("candidate mutates deployed schema period %s", from)
		}
	}

	asOfDate := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), asOf.UTC().Day(), 0, 0, 0, 0, time.UTC)
	result := Result{AcceptedPeriods: len(accepted)}
	for _, period := range periods {
		if _, exists := accepted[period.From]; exists {
			continue
		}
		activation, _ := time.Parse("2006-01-02", period.From)
		if !activation.After(asOfDate) {
			return Result{}, fmt.Errorf(
				"new schema period %s must activate after current UTC date %s",
				period.From,
				asOfDate.Format("2006-01-02"),
			)
		}
		result.NewPeriods = append(result.NewPeriods, period.From)
	}
	sort.Strings(result.NewPeriods)
	return result, nil
}

func parsePeriod(period Period) (time.Time, error) {
	date, err := time.Parse("2006-01-02", period.From)
	if err != nil {
		return time.Time{}, fmt.Errorf("from date is invalid: %w", err)
	}
	if period.Store == "" || period.ObjectStore == "" || period.Schema == "" || period.Index.Prefix == "" || period.Index.Period == "" {
		return time.Time{}, errors.New("schema period omits store, object_store, schema, or index identity")
	}
	return date, nil
}
