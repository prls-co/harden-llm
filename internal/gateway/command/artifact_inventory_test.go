package command

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-060

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/postgres"
)

func TestArtifactInventoryAudit(t *testing.T) {
	now := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	store := inventoryStoreFixture{references: []postgres.ArtifactInventoryReference{
		{ObjectKey: "llm-traces/a/available.json", Source: "metadata", State: "available"},
		{ObjectKey: "llm-traces/a/pending.json", Source: "operation", State: "object_applied"},
	}}
	objects := &inventoryObjectFixture{pages: []artifacts.InventoryPage{{Objects: []artifacts.InventoryObject{
		{Key: "llm-traces/a/available.json", SizeBytes: 1, LastModified: now.Add(-time.Hour)},
		{Key: "llm-traces/a/pending.json", SizeBytes: 1, LastModified: now.Add(-time.Hour)},
		{Key: "llm-traces/a/young.json", SizeBytes: 1, LastModified: now.Add(-time.Minute)},
	}}}}
	report, err := AuditArtifactInventory(context.Background(), ArtifactInventoryConfig{
		Store: store, Objects: objects, Prefix: "llm-traces/", Now: now,
	})
	if err != nil || !report.Healthy || report.ReferencedObjects != 2 || report.ActiveOperationObjects != 1 ||
		report.UnreferencedYoungObjects != 1 || report.UnreferencedAgedObjects != 0 || report.MissingAvailableObjects != 0 {
		t.Fatalf("healthy inventory = %#v, %v", report, err)
	}
	if !reflect.DeepEqual(objects.calls, []inventoryCall{{prefix: "llm-traces/", limit: 1000}}) {
		t.Fatalf("inventory calls = %#v", objects.calls)
	}

	objects = &inventoryObjectFixture{pages: []artifacts.InventoryPage{{Objects: []artifacts.InventoryObject{{
		Key: "llm-traces/a/orphan.json", SizeBytes: 1, LastModified: now.Add(-time.Hour),
	}}}}}
	report, err = AuditArtifactInventory(context.Background(), ArtifactInventoryConfig{
		Store: store, Objects: objects, Prefix: "llm-traces/", Now: now,
	})
	if err == nil || report.Healthy || report.UnreferencedAgedObjects != 1 || report.MissingAvailableObjects != 1 {
		t.Fatalf("unhealthy inventory = %#v, %v", report, err)
	}
}

type inventoryStoreFixture struct {
	references []postgres.ArtifactInventoryReference
	truncated  bool
	err        error
}

func (fixture inventoryStoreFixture) ArtifactInventoryReferences(context.Context, int) ([]postgres.ArtifactInventoryReference, bool, error) {
	return fixture.references, fixture.truncated, fixture.err
}

type inventoryCall struct {
	prefix, continuation string
	limit                int32
}

type inventoryObjectFixture struct {
	pages []artifacts.InventoryPage
	calls []inventoryCall
}

func (fixture *inventoryObjectFixture) Inventory(_ context.Context, prefix, continuation string, limit int32) (artifacts.InventoryPage, error) {
	fixture.calls = append(fixture.calls, inventoryCall{prefix: prefix, continuation: continuation, limit: limit})
	if len(fixture.pages) == 0 {
		return artifacts.InventoryPage{}, errors.New("unexpected inventory page")
	}
	page := fixture.pages[0]
	fixture.pages = fixture.pages[1:]
	return page, nil
}
