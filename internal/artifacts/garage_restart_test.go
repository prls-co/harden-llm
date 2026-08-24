//go:build integration && garageexclusive

package artifacts

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-043 TEST-053

import (
	"context"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func TestGarageRestartPersistence(t *testing.T) {
	service, fixture := integrationtest.StartExclusiveGarage(t)
	store, err := NewGarage(Config{
		Endpoint: fixture.Endpoint, ExternalEndpoint: fixture.Endpoint,
		Bucket: fixture.Bucket, Region: fixture.Region,
		AccessKeyID: fixture.AccessKeyID, SecretAccessKey: fixture.SecretAccessKey,
		OperationTimeout: 3 * time.Second, MaxPresignTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Scoped("llm-traces/restart-owner/")
	if err != nil {
		t.Fatal(err)
	}
	const key = "llm-traces/restart-owner/restart.json"
	const content = `{"survives":"restart"}`
	if _, err := owner.Put(context.Background(), key, []byte(content), "application/json"); err != nil {
		t.Fatal(err)
	}
	service.Restart(t)
	restartedStore, err := NewGarage(Config{
		Endpoint: "http://" + service.Endpoint, ExternalEndpoint: "http://" + service.Endpoint,
		Bucket: fixture.Bucket, Region: fixture.Region,
		AccessKeyID: fixture.AccessKeyID, SecretAccessKey: fixture.SecretAccessKey,
		OperationTimeout: 3 * time.Second, MaxPresignTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	restartedOwner, err := restartedStore.Scoped("llm-traces/restart-owner/")
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := restartedOwner.Get(context.Background(), key); err != nil || string(got) != content {
		t.Fatalf("object did not survive Garage restart: %s %v", got, err)
	}
}
