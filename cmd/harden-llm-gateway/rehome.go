package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

type identityOwnerMapping struct {
	LocalOwnerID string `json:"localOwnerId"`
	AccountID    string `json:"accountId"`
}

type identityMappingFile struct {
	Owners []identityOwnerMapping `json:"owners"`
}

func runRehomeIdentities(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("rehome-identities", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mappingPath := flags.String("mapping-file", "", "JSON file mapping local owner IDs to Control Plane account UUIDs")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*mappingPath) == "" {
		return errors.New("rehome-identities: --mapping-file is required")
	}
	mapping, err := readIdentityMapping(*mappingPath)
	if err != nil {
		return err
	}
	if getenv == nil {
		return errors.New("rehome-identities: environment reader is required")
	}
	databaseURL := requiredEnvironment(getenv, databaseURLEnvironment)
	activeKeyID := requiredEnvironment(getenv, activeEncryptionKeyEnvironment)
	keys, err := parseEncryptionKeys(getenv(encryptionKeysEnvironment))
	if err != nil || databaseURL == "" {
		return errors.New("rehome-identities: database and credential encryption configuration are required")
	}
	vault, err := profiles.NewCredentialVault(activeKeyID, keys, nil)
	if err != nil {
		return errors.New("rehome-identities: credential encryption configuration is invalid")
	}
	endpoint := requiredEnvironment(getenv, artifactEndpointEnvironment)
	bucket := requiredEnvironment(getenv, artifactBucketEnvironment)
	accessKey := requiredEnvironment(getenv, artifactAccessKeyEnvironment)
	secretKey := requiredEnvironment(getenv, artifactSecretKeyEnvironment)
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return errors.New("rehome-identities: Garage endpoint, bucket, and credentials are required")
	}
	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: endpoint, Bucket: bucket, Region: "garage", AccessKeyID: accessKey,
		SecretAccessKey: secretKey, MaxPresignTTL: time.Minute,
	})
	if err != nil {
		return errors.New("rehome-identities: Garage configuration is invalid")
	}
	operationContext, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	store, err := postgres.Open(operationContext, databaseURL)
	if err != nil {
		return errors.New("rehome-identities: application database is unavailable")
	}
	defer store.Close()
	if err := store.MigrateThrough(operationContext, 9); err != nil {
		return fmt.Errorf("rehome-identities: prepare owner-key migration: %w", err)
	}
	owners, usersTable, err := store.LocalIdentityOwners(operationContext)
	if err != nil || !usersTable {
		return errors.New("rehome-identities: legacy identity table is unavailable; this command must run before identity removal")
	}
	sourceState := identityMappingMatches(owners, mapping, false)
	targetState := identityMappingMatches(owners, mapping, true)
	if !sourceState && !targetState {
		return errors.New("rehome-identities: mapping must cover every local identity and may not consolidate accounts")
	}
	if sourceState {
		credentials, err := reencryptedCredentials(operationContext, store, vault, mapping)
		if err != nil {
			return errors.New("rehome-identities: one or more provider credentials could not be re-encrypted")
		}
		for source, target := range mapping {
			if err := verifyAvailableArtifacts(operationContext, store, garage, source); err != nil {
				return fmt.Errorf("rehome-identities: source artifact verification failed for owner %q", source)
			}
			if _, err := copyOwnerObjects(operationContext, garage, source, target); err != nil {
				return fmt.Errorf("rehome-identities: object copy failed for owner %q", source)
			}
		}
		if err := store.RehomeOwnerIDs(operationContext, mapping, credentials); err != nil {
			return fmt.Errorf("rehome-identities: database owner rehome failed: %w", err)
		}
	} else {
		// The database transaction is all-or-nothing. This state only occurs
		// when a prior invocation committed its owner changes before stopping.
		for source, target := range mapping {
			if _, err := copyOwnerObjects(operationContext, garage, source, target); err != nil {
				return fmt.Errorf("rehome-identities: object copy resume failed for owner %q", source)
			}
		}
	}
	if err := store.ValidateNoLegacyReferences(operationContext, mapping); err != nil {
		return fmt.Errorf("rehome-identities: legacy references remain: %w", err)
	}
	for _, target := range mappedAccountIDs(mapping) {
		if err := verifyAvailableArtifacts(operationContext, store, garage, target); err != nil {
			return errors.New("rehome-identities: copied artifact verification failed")
		}
	}
	deleted := 0
	for source, target := range mapping {
		if source == target {
			continue
		}
		count, err := deleteOwnerObjects(operationContext, garage, source)
		if err != nil {
			return fmt.Errorf("rehome-identities: old object-prefix cleanup failed for owner %q", source)
		}
		deleted += count
	}
	if err := store.MigrateThrough(operationContext, 10); err != nil {
		return fmt.Errorf("rehome-identities: remove local identity tables: %w", err)
	}
	if err := store.Ready(operationContext); err != nil {
		return fmt.Errorf("rehome-identities: final database verification failed: %w", err)
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"owners": len(mapping), "migrations": []int{9, 10}, "deletedLegacyObjects": deleted,
		"databaseReady": true,
	})
}

func readIdentityMapping(path string) (map[string]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil || len(contents) == 0 || len(contents) > 64<<10 {
		return nil, errors.New("rehome-identities: mapping file is unavailable or exceeds 64 KiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var document identityMappingFile
	if err := decoder.Decode(&document); err != nil || len(document.Owners) == 0 || len(document.Owners) > 100 {
		return nil, errors.New("rehome-identities: mapping file must contain between 1 and 100 owner records")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("rehome-identities: mapping file must contain one JSON document")
	}
	mapping := make(map[string]string, len(document.Owners))
	targets := make(map[string]struct{}, len(document.Owners))
	for _, owner := range document.Owners {
		if strings.TrimSpace(owner.LocalOwnerID) == "" || owner.LocalOwnerID != strings.TrimSpace(owner.LocalOwnerID) || auth.ValidateAccountID(owner.AccountID) != nil {
			return nil, errors.New("rehome-identities: owner IDs and Control Plane account UUIDs must be valid")
		}
		if _, exists := mapping[owner.LocalOwnerID]; exists {
			return nil, errors.New("rehome-identities: mapping contains a duplicate local owner")
		}
		if _, exists := targets[owner.AccountID]; exists {
			return nil, errors.New("rehome-identities: multiple local owners cannot be consolidated into one account")
		}
		mapping[owner.LocalOwnerID] = owner.AccountID
		targets[owner.AccountID] = struct{}{}
	}
	return mapping, nil
}

func identityMappingMatches(actual []string, mapping map[string]string, targets bool) bool {
	if len(actual) != len(mapping) {
		return false
	}
	expected := make(map[string]struct{}, len(mapping))
	for source, target := range mapping {
		if targets {
			expected[target] = struct{}{}
		} else {
			expected[source] = struct{}{}
		}
	}
	for _, owner := range actual {
		if _, ok := expected[owner]; !ok {
			return false
		}
	}
	return true
}

func mappedAccountIDs(mapping map[string]string) []string {
	ids := make([]string, 0, len(mapping))
	seen := make(map[string]struct{}, len(mapping))
	for _, accountID := range mapping {
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		ids = append(ids, accountID)
	}
	return ids
}

func reencryptedCredentials(ctx context.Context, store *postgres.Store, vault *profiles.CredentialVault, mapping map[string]string) (map[string][]postgres.CredentialRecord, error) {
	result := make(map[string][]postgres.CredentialRecord, len(mapping))
	for source, target := range mapping {
		stored, err := store.Credentials(ctx, source)
		if err != nil {
			return nil, err
		}
		converted := make([]postgres.CredentialRecord, 0, len(stored))
		for _, credential := range stored {
			oldCiphertext := profiles.EncryptedCredential{
				SchemaVersion: 1, Algorithm: "AES-256-GCM", KeyID: credential.KeyID,
				Nonce:      base64.RawURLEncoding.EncodeToString(credential.Nonce),
				Ciphertext: base64.RawURLEncoding.EncodeToString(credential.Ciphertext),
			}
			payload, err := vault.Open(oldCiphertext, profiles.CredentialBinding{
				OwnerID: source, CredentialID: credential.ID, Origin: credential.Origin,
			})
			if err != nil {
				return nil, err
			}
			newCiphertext, err := vault.Seal(payload, profiles.CredentialBinding{
				OwnerID: target, CredentialID: credential.ID, Origin: credential.Origin,
			})
			if err != nil {
				return nil, err
			}
			nonce, err := base64.RawURLEncoding.DecodeString(newCiphertext.Nonce)
			if err != nil {
				return nil, err
			}
			ciphertext, err := base64.RawURLEncoding.DecodeString(newCiphertext.Ciphertext)
			if err != nil {
				return nil, err
			}
			credential.OwnerID, credential.KeyID = target, newCiphertext.KeyID
			credential.Nonce, credential.Ciphertext = nonce, ciphertext
			converted = append(converted, credential)
		}
		result[source] = converted
	}
	return result, nil
}

func copyOwnerObjects(ctx context.Context, garage *artifacts.GarageStore, source, target string) (int, error) {
	if source == target {
		return 0, nil
	}
	oldPrefix, newPrefix := "llm-traces/"+source+"/", "llm-traces/"+target+"/"
	token, copied := "", 0
	for {
		page, err := garage.Inventory(ctx, oldPrefix, token, 1000)
		if err != nil {
			return copied, err
		}
		for _, object := range page.Objects {
			content, sourceRef, err := garage.Get(ctx, object.Key)
			if err != nil || sourceRef.Key != object.Key || sourceRef.SizeBytes != object.SizeBytes || sourceRef.ContentType != "application/json" {
				return copied, errors.New("source object failed integrity validation")
			}
			targetKey := newPrefix + strings.TrimPrefix(object.Key, oldPrefix)
			targetRef, err := garage.Put(ctx, targetKey, content, "application/json")
			if err != nil || targetRef.SHA256 != sourceRef.SHA256 || targetRef.SizeBytes != sourceRef.SizeBytes {
				return copied, errors.New("target object failed integrity validation")
			}
			copied++
		}
		if page.ContinuationToken == "" {
			return copied, nil
		}
		if page.ContinuationToken == token {
			return copied, errors.New("Garage inventory did not advance")
		}
		token = page.ContinuationToken
	}
}

func deleteOwnerObjects(ctx context.Context, garage *artifacts.GarageStore, ownerID string) (int, error) {
	prefix, token, deleted := "llm-traces/"+ownerID+"/", "", 0
	for {
		page, err := garage.Inventory(ctx, prefix, token, 1000)
		if err != nil {
			return deleted, err
		}
		keys := make([]string, 0, len(page.Objects))
		for _, object := range page.Objects {
			keys = append(keys, object.Key)
		}
		if len(keys) > 0 {
			if err := garage.DeleteMany(ctx, keys); err != nil {
				return deleted, err
			}
			deleted += len(keys)
		}
		if page.ContinuationToken == "" {
			break
		}
		if page.ContinuationToken == token {
			return deleted, errors.New("Garage inventory did not advance")
		}
		token = page.ContinuationToken
	}
	remaining, err := garage.Inventory(ctx, prefix, "", 1)
	if err != nil || len(remaining.Objects) != 0 || remaining.ContinuationToken != "" {
		return deleted, errors.New("legacy object prefix is not empty")
	}
	return deleted, nil
}

func verifyAvailableArtifacts(ctx context.Context, store *postgres.Store, garage *artifacts.GarageStore, ownerID string) error {
	records, err := store.ArtifactsForOwner(ctx, ownerID)
	if err != nil {
		return err
	}
	prefix := "llm-traces/" + ownerID + "/"
	for _, record := range records {
		if !strings.HasPrefix(record.ObjectKey, prefix) {
			return errors.New("artifact metadata uses an unsupported owner prefix")
		}
		if record.State != "available" {
			continue
		}
		ref, found, err := garage.Inspect(ctx, record.ObjectKey)
		if err != nil || !found || ref.SHA256 != record.SHA256 || ref.SizeBytes != record.SizeBytes || ref.ContentType != record.ContentType {
			return errors.New("available artifact failed integrity verification")
		}
	}
	return nil
}
