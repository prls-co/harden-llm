package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway/command"
	"github.com/prls-co/harden-llm/internal/postgres"
)

const artifactInventoryTimeout = 10 * time.Minute

func runArtifactInventory(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string) error {
	if len(args) != 0 {
		return errors.New("audit-artifacts: no arguments are accepted")
	}
	if getenv == nil {
		return errors.New("audit-artifacts: environment reader is required")
	}
	storage := struct {
		database, endpoint, external, bucket, accessKey, secretKey string
	}{
		database:  strings.TrimSpace(getenv(databaseURLEnvironment)),
		endpoint:  strings.TrimSpace(getenv(artifactEndpointEnvironment)),
		external:  strings.TrimSpace(getenv(artifactExternalEnvironment)),
		bucket:    strings.TrimSpace(getenv(artifactBucketEnvironment)),
		accessKey: strings.TrimSpace(getenv(artifactAccessKeyEnvironment)),
		secretKey: strings.TrimSpace(getenv(artifactSecretKeyEnvironment)),
	}
	for _, item := range []struct{ name, value string }{
		{databaseURLEnvironment, storage.database}, {artifactEndpointEnvironment, storage.endpoint},
		{artifactExternalEnvironment, storage.external}, {artifactBucketEnvironment, storage.bucket},
		{artifactAccessKeyEnvironment, storage.accessKey}, {artifactSecretKeyEnvironment, storage.secretKey},
	} {
		if item.value == "" {
			return fmt.Errorf("audit-artifacts: %s is required", item.name)
		}
	}
	commandContext, cancel := context.WithTimeout(ctx, artifactInventoryTimeout)
	defer cancel()
	store, err := postgres.Open(commandContext, storage.database)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Ready(commandContext); err != nil {
		return err
	}
	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: storage.endpoint, ExternalEndpoint: storage.external, Bucket: storage.bucket,
		Region: "garage", AccessKeyID: storage.accessKey, SecretAccessKey: storage.secretKey,
	})
	if err != nil {
		return err
	}
	report, auditErr := command.AuditArtifactInventory(commandContext, command.ArtifactInventoryConfig{
		Store: store, Objects: garage, Prefix: "llm-traces/",
	})
	if report.SchemaVersion != 0 {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return err
		}
	}
	return auditErr
}
