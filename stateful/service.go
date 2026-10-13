package stateful

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"time"
)

type HistoryConfig struct{ Enabled bool }
type Config struct {
	Client                     ClientOptions
	Pool                       *pgxpool.Pool
	Presets                    Catalog
	Vault                      *CredentialVault
	History                    HistoryConfig
	CacheTTL                   time.Duration
	Artifacts                  *ArtifactStore
	ArtifactConfigurationError *Diagnostic
}
type Service struct {
	config                Config
	effects               hardenllm.Options
	now                   func() time.Time
	newID                 func() (string, error)
	afterCredentialInsert func() error
	afterBundleDelete     func() error
	beforeArtifactDelete  func(string) error
	onClearBatch          func(int)
}

func NewService(config Config) (*Service, error) {
	if config.CacheTTL == 0 {
		config.CacheTTL = defaultCacheTTL
	}
	if config.CacheTTL < 0 {
		return nil, &OperationError{Code: "reference_configuration_error", Message: "Cache TTL must be positive."}
	}
	if config.ArtifactConfigurationError != nil {
		diagnostic := *config.ArtifactConfigurationError
		config.ArtifactConfigurationError = &diagnostic
	}
	effects, err := newEffects(config.Client)
	if err != nil {
		return nil, err
	}
	if _, err := hardenllm.New(effects); err != nil {
		return nil, err
	}
	if config.Presets == nil {
		config.Presets = Catalog{}
	}
	data, err := json.Marshal(config.Presets)
	if err != nil {
		return nil, validationFailure("catalog", "Preset catalog is not JSON safe.")
	}
	config.Presets, err = ParseCatalog(data)
	if err != nil {
		return nil, err
	}
	return &Service{config: config, effects: effects, now: time.Now, newID: func() (string, error) { id, err := uuid.NewRandom(); return id.String(), err }}, nil
}
func (s *Service) ClientForKey(hash string) (*hardenllm.Client, error) {
	if !validKeyHash(hash) {
		return nil, &OperationError{Code: "invalid_api_key", Message: "An authenticated access-key scope is required."}
	}
	options := s.effects
	options.Cache = newPostgresCache(s.config.Pool, hash, credentialNamespace(options.Connections, nil), s.config.CacheTTL, s.now)
	return hardenllm.New(options)
}
func checkScope(hash string) error {
	if !validKeyHash(hash) {
		return &OperationError{Code: "invalid_api_key", Message: "An authenticated access-key scope is required."}
	}
	return nil
}
func storageFailure(err error) error {
	if err == nil {
		return nil
	}
	var op *OperationError
	var val *ValidationError
	if errors.As(err, &op) || errors.As(err, &val) {
		return err
	}
	return &OperationError{Code: "storage_unavailable", Message: "Reference PostgreSQL operation failed; inspect correlated service diagnostics.", cause: err}
}
