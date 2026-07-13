// Package command implements bounded administrative gateway commands.
package command

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/postgres"
)

type BootstrapUserConfig struct {
	DatabaseURL string
	OwnerID     string
	Email       string
	Password    string
	Clock       func() time.Time
	Random      io.Reader
}

// BootstrapUser migrates the application database and creates one local user.
func BootstrapUser(ctx context.Context, config BootstrapUserConfig) (postgres.User, error) {
	if strings.TrimSpace(config.DatabaseURL) == "" {
		return postgres.User{}, errors.New("gateway command: database URL is required")
	}
	store, err := postgres.Open(ctx, config.DatabaseURL)
	if err != nil {
		return postgres.User{}, err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return postgres.User{}, err
	}
	service, err := auth.NewService(auth.Config{
		Store: store, SessionTTL: time.Minute, Clock: config.Clock, Random: config.Random,
	})
	if err != nil {
		return postgres.User{}, err
	}
	return service.BootstrapUser(ctx, config.OwnerID, config.Email, config.Password)
}
