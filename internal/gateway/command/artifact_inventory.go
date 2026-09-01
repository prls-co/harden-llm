package command

import (
	"context"
	"errors"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/postgres"
)

const (
	defaultArtifactInventoryLimit = 10_000
	artifactInventoryPageSize     = 1_000
	defaultArtifactMinimumAge     = 15 * time.Minute
)

type ArtifactInventoryStore interface {
	ArtifactInventoryReferences(context.Context, int) ([]postgres.ArtifactInventoryReference, bool, error)
}

type ArtifactInventoryObjects interface {
	Inventory(context.Context, string, string, int32) (artifacts.InventoryPage, error)
}

type ArtifactInventoryConfig struct {
	Store      ArtifactInventoryStore
	Objects    ArtifactInventoryObjects
	Prefix     string
	Limit      int
	MinimumAge time.Duration
	Now        time.Time
}

type ArtifactInventoryReport struct {
	SchemaVersion             int  `json:"schemaVersion"`
	ScannedObjects            int  `json:"scannedObjects"`
	MetadataReferences        int  `json:"metadataReferences"`
	ActiveOperationReferences int  `json:"activeOperationReferences"`
	ReferencedObjects         int  `json:"referencedObjects"`
	ActiveOperationObjects    int  `json:"activeOperationObjects"`
	MissingAvailableObjects   int  `json:"missingAvailableObjects"`
	UnreferencedYoungObjects  int  `json:"unreferencedYoungObjects"`
	UnreferencedAgedObjects   int  `json:"unreferencedAgedObjects"`
	Truncated                 bool `json:"truncated"`
	Healthy                   bool `json:"healthy"`
}

// AuditArtifactInventory compares a bounded read-only Garage listing with the
// live PostgreSQL ownership snapshot. Its report contains counts only.
func AuditArtifactInventory(ctx context.Context, config ArtifactInventoryConfig) (ArtifactInventoryReport, error) {
	if config.Store == nil || config.Objects == nil || config.Prefix == "" {
		return ArtifactInventoryReport{}, errors.New("gateway command: artifact inventory stores are required")
	}
	if config.Limit == 0 {
		config.Limit = defaultArtifactInventoryLimit
	}
	if config.Limit < 1 || config.Limit > 100_000 {
		return ArtifactInventoryReport{}, errors.New("gateway command: artifact inventory limit is invalid")
	}
	if config.MinimumAge == 0 {
		config.MinimumAge = defaultArtifactMinimumAge
	}
	if config.MinimumAge < time.Minute || config.MinimumAge > 24*time.Hour {
		return ArtifactInventoryReport{}, errors.New("gateway command: artifact inventory minimum age is invalid")
	}
	if config.Now.IsZero() {
		config.Now = time.Now().UTC()
	} else {
		config.Now = config.Now.UTC()
	}

	references, referencesTruncated, err := config.Store.ArtifactInventoryReferences(ctx, config.Limit)
	if err != nil {
		return ArtifactInventoryReport{}, err
	}
	metadata := make(map[string]string)
	operations := make(map[string]struct{})
	for _, reference := range references {
		switch reference.Source {
		case "metadata":
			metadata[reference.ObjectKey] = reference.State
		case "operation":
			operations[reference.ObjectKey] = struct{}{}
		default:
			return ArtifactInventoryReport{}, errors.New("gateway command: artifact inventory reference is invalid")
		}
	}

	report := ArtifactInventoryReport{
		SchemaVersion: 1, MetadataReferences: len(metadata),
		ActiveOperationReferences: len(operations), Truncated: referencesTruncated,
	}
	objects := make(map[string]artifacts.InventoryObject)
	continuation := ""
	for len(objects) < config.Limit {
		remaining := config.Limit - len(objects)
		pageSize := artifactInventoryPageSize
		if remaining < pageSize {
			pageSize = remaining
		}
		page, listErr := config.Objects.Inventory(ctx, config.Prefix, continuation, int32(pageSize))
		if listErr != nil {
			return report, errors.New("gateway command: Garage artifact inventory failed")
		}
		for _, object := range page.Objects {
			if _, duplicate := objects[object.Key]; duplicate {
				return report, errors.New("gateway command: Garage artifact inventory contains a duplicate key")
			}
			objects[object.Key] = object
		}
		if page.ContinuationToken == "" {
			continuation = ""
			break
		}
		continuation = page.ContinuationToken
	}
	if continuation != "" && len(objects) == config.Limit {
		report.Truncated = true
	}
	report.ScannedObjects = len(objects)
	cutoff := config.Now.Add(-config.MinimumAge)
	for key, object := range objects {
		if _, ok := metadata[key]; ok {
			report.ReferencedObjects++
			continue
		}
		if _, ok := operations[key]; ok {
			report.ReferencedObjects++
			report.ActiveOperationObjects++
			continue
		}
		if object.LastModified.IsZero() || !object.LastModified.After(cutoff) {
			report.UnreferencedAgedObjects++
		} else {
			report.UnreferencedYoungObjects++
		}
	}
	for key, state := range metadata {
		if state == "available" {
			if _, ok := objects[key]; !ok {
				report.MissingAvailableObjects++
			}
		}
	}
	report.Healthy = !report.Truncated && report.MissingAvailableObjects == 0 && report.UnreferencedAgedObjects == 0
	if !report.Healthy {
		return report, errors.New("gateway command: artifact inventory found unresolved anomalies")
	}
	return report, nil
}
