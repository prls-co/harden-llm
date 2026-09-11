package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func runSyncProfiles(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("sync-profiles", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "local account email")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *email == "" {
		return errors.New("sync-profiles: --email is required")
	}
	decoder := json.NewDecoder(io.LimitReader(stdin, 2<<20))
	decoder.DisallowUnknownFields()
	var config gateway.SharedProfiles
	if err := decoder.Decode(&config); err != nil {
		return errors.New("sync-profiles: invalid configuration JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("sync-profiles: unexpected trailing configuration")
	}
	keys, err := parseEncryptionKeys(getenv(encryptionKeysEnvironment))
	if err != nil {
		return errors.New("sync-profiles: invalid encryption configuration")
	}
	vault, err := profiles.NewCredentialVault(getenv(activeEncryptionKeyEnvironment), keys, nil)
	if err != nil {
		return errors.New("sync-profiles: invalid credential vault")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	store, err := postgres.Open(ctx, getenv(databaseURLEnvironment))
	if err != nil {
		return errors.New("sync-profiles: database unavailable")
	}
	defer store.Close()
	user, err := store.UserByEmail(ctx, *email)
	if err != nil {
		return errors.New("sync-profiles: local account not found")
	}
	result, err := gateway.ApplySharedProfilesWithResult(ctx, store, vault, user.ID, config)
	if err != nil {
		return errors.New("sync-profiles: configuration could not be applied")
	}
	return json.NewEncoder(stdout).Encode(result)
}
