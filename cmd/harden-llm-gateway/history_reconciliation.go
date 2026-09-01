package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/command"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/traces"
)

const historyReconciliationTimeout = 10 * time.Minute

func runHistoryReconciliation(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("reconcile-history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ownerID := flags.String("owner-id", "", "exact owner scope")
	allOwners := flags.Bool("all-owners", false, "explicitly scan every owner")
	apply := flags.Bool("apply", false, "apply the unchanged dry-run plan")
	digest := flags.String("digest", "", "exact dry-run plan digest")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("reconcile-history: invalid arguments: %w", err)
	}
	if flags.NArg() != 0 || (strings.TrimSpace(*ownerID) == "") == !*allOwners {
		return errors.New("reconcile-history: select exactly one of --owner-id or --all-owners")
	}
	if *apply && len(strings.TrimSpace(*digest)) != 64 {
		return errors.New("reconcile-history: --apply requires the exact 64-character --digest")
	}
	if getenv == nil {
		return errors.New("reconcile-history: environment reader is required")
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
		{databaseURLEnvironment, storage.database},
		{artifactEndpointEnvironment, storage.endpoint},
		{artifactExternalEnvironment, storage.external},
		{artifactBucketEnvironment, storage.bucket},
		{artifactAccessKeyEnvironment, storage.accessKey},
		{artifactSecretKeyEnvironment, storage.secretKey},
	} {
		if item.value == "" {
			return fmt.Errorf("reconcile-history: %s is required", item.name)
		}
	}
	commandContext, cancel := context.WithTimeout(ctx, historyReconciliationTimeout)
	defer cancel()
	store, err := postgres.Open(commandContext, storage.database)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(commandContext); err != nil {
		return err
	}
	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: storage.endpoint, ExternalEndpoint: storage.external,
		Bucket: storage.bucket, Region: "garage",
		AccessKeyID: storage.accessKey, SecretAccessKey: storage.secretKey,
	})
	if err != nil {
		return err
	}
	coordinator, err := gateway.NewArtifactCoordinator(gateway.ArtifactCoordinatorConfig{
		Store: store,
		Scope: func(owner string) (gateway.ArtifactObjectAccess, error) {
			prefix := "llm-traces/" + traces.SafeObjectKeyComponent(owner) + "/"
			return garage.Scoped(prefix)
		},
	})
	if err != nil {
		return err
	}
	report, reconcileErr := command.ReconcileHistory(commandContext, command.HistoryReconciliationConfig{
		Store: store, Artifacts: coordinator, OwnerID: strings.TrimSpace(*ownerID), AllOwners: *allOwners,
		Apply: *apply, PlanDigest: strings.ToLower(strings.TrimSpace(*digest)),
	})
	if report.SchemaVersion != 0 {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return err
		}
	}
	return reconcileErr
}
