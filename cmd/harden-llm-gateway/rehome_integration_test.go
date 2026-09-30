//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022 TEST-053
func TestRehomeIdentitiesPreservesDatabaseCredentialsAndGarageArtifacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	databaseFixture, databaseURL := integrationtest.PostgresLease(t)
	_, garageFixture := integrationtest.GarageLease(t)

	leaseID := strings.TrimPrefix(databaseFixture.Database, "harden_test_")
	if len(leaseID) < 12 {
		t.Fatalf("test database lease ID is too short: %q", leaseID)
	}
	oldOwner := "operator-" + leaseID
	accountID := "11111111-1111-4111-8111-" + leaseID[:12]
	const credentialID = "provider-primary"
	const origin = "https://api.example.test"
	objectKey := "llm-traces/" + oldOwner + "/trace-1/artifact-1.json"
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	key := []byte(strings.Repeat("k", 32))
	keyJSON, err := json.Marshal(map[string]string{"primary": base64.RawURLEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	vault, err := profiles.NewCredentialVault("primary", map[string][]byte{"primary": key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldBinding := profiles.CredentialBinding{OwnerID: oldOwner, CredentialID: credentialID, Origin: origin}
	encrypted, err := vault.Seal(profiles.CredentialPayload{APIKey: "fixture-provider-key", Headers: map[string]string{"X-Fixture": "retained"}}, oldBinding)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: garageFixture.Endpoint, Bucket: garageFixture.Bucket, Region: garageFixture.Region,
		AccessKeyID: garageFixture.AccessKeyID, SecretAccessKey: garageFixture.SecretAccessKey,
		MaxPresignTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"traceId":"trace-1","status":"retained"}`)
	if _, err := garage.Put(ctx, objectKey, content, "application/json"); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{oldOwner, accountID} {
		owner := owner
		t.Cleanup(func() {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			if cleanupErr := integrationtest.DeleteGarageOwnerTraceArtifacts(cleanupContext, garageFixture, owner); cleanupErr != nil {
				t.Errorf("clean test Garage owner %q: %v", owner, cleanupErr)
			}
		})
	}

	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateThrough(ctx, 9); err != nil {
		t.Fatal(err)
	}
	store.Close()
	database, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(ctx, `INSERT INTO users(id,email,password_hash,created_at,updated_at) VALUES($1,'operator@example.test','$argon2id$fixture',$2,$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO user_sessions(id,owner_id,token_digest,expires_at,created_at) VALUES('session-1',$1,decode(repeat('ab',32),'hex'),$2,$3)`, oldOwner, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_endpoint_credentials(owner_id,credential_id,key_id,nonce,ciphertext,normalized_origin,metadata,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'{"schemaVersion":1}', $7,$7)`, oldOwner, credentialID, encrypted.KeyID, nonce, ciphertext, origin, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_profiles(owner_id,profile_id,credential_id,document,created_at,updated_at) VALUES($1,'Primary',$2,'{"profileId":"Primary"}',$3,$3)`, oldOwner, credentialID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_client_state(owner_id,document,updated_at) VALUES($1,'{"workspace":"retained"}',$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_runs(owner_id,run_id,profile_id,trace_id,status,request,result,started_at,completed_at) VALUES($1,'run-1','Primary','trace-1','succeeded','{"prompt":"retained"}','{"output":"retained"}',$2,$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_traces(owner_id,trace_id,run_id,record,created_at,updated_at) VALUES($1,'trace-1','run-1','{"traceId":"trace-1"}',$2,$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_trace_observations(owner_id,trace_id,sequence,observation_type,data,created_at) VALUES($1,'trace-1',0,'fixture','{"value":"retained"}',$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if _, err := database.Exec(ctx, `INSERT INTO llm_artifacts(owner_id,trace_id,artifact_id,kind,object_key,content_type,sha256,size_bytes,state,verified_at,created_at,updated_at) VALUES($1,'trace-1','artifact-1','trace',$2,'application/json',$3,$4,'available',$5,$5,$5)`, oldOwner, objectKey, hex.EncodeToString(digest[:]), len(content), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_operation_cache(owner_id,cache_version,operation_hash,result,created_at,updated_at) VALUES($1,'cache-v1','operation-1','{"output":"retained"}',$2,$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_artifact_delete_batches(batch_id,owner_id,scope,state,expected_artifact_count,deleted_run_count,created_at,updated_at,completed_at) VALUES('batch-1',$1,'owner','completed',0,0,$2,$2,$2)`, oldOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO llm_artifact_operations(operation_id,batch_id,action,state,owner_id,run_id,trace_id,artifact_id,kind,object_key,content_type,sha256,size_bytes,next_attempt_at,created_at,updated_at,completed_at) VALUES(repeat('b',64),NULL,'publish','completed',$1,'run-1','trace-1','artifact-1','trace',$2,'application/json',$3,$4,$5,$5,$5,$5)`, oldOwner, objectKey, hex.EncodeToString(digest[:]), len(content), now); err != nil {
		t.Fatal(err)
	}

	mappingPath := t.TempDir() + "/identity-map.json"
	mapping, err := json.Marshal(identityMappingFile{Owners: []identityOwnerMapping{{LocalOwnerID: oldOwner, AccountID: accountID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mappingPath, mapping, 0o600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		databaseURLEnvironment:         databaseURL,
		encryptionKeysEnvironment:      string(keyJSON),
		activeEncryptionKeyEnvironment: "primary",
		artifactEndpointEnvironment:    garageFixture.Endpoint,
		artifactBucketEnvironment:      garageFixture.Bucket,
		artifactAccessKeyEnvironment:   garageFixture.AccessKeyID,
		artifactSecretKeyEnvironment:   garageFixture.SecretAccessKey,
	}
	var output strings.Builder
	if err := runRehomeIdentities(ctx, []string{"--mapping-file", mappingPath}, &output, func(name string) string { return environment[name] }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"databaseReady":true`) || !strings.Contains(output.String(), `"deletedLegacyObjects":1`) {
		t.Fatalf("migration result did not confirm preserved data and cleanup: %s", output.String())
	}

	store, err = postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.LocalIdentityOwners(ctx); err != nil || present {
		t.Fatalf("local identity table remains present=%v err=%v", present, err)
	}
	profile, err := store.Profile(ctx, accountID, "Primary")
	if err != nil || !strings.Contains(string(profile.Document), "Primary") {
		t.Fatalf("account-owned profile was not preserved: %#v %v", profile, err)
	}
	credential, err := store.Credential(ctx, accountID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := vault.Open(profiles.EncryptedCredential{
		SchemaVersion: 1, Algorithm: "AES-256-GCM", KeyID: credential.KeyID,
		Nonce: base64.RawURLEncoding.EncodeToString(credential.Nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(credential.Ciphertext),
	}, profiles.CredentialBinding{OwnerID: accountID, CredentialID: credentialID, Origin: origin})
	if err != nil || reopened.APIKey != "fixture-provider-key" || reopened.Headers["X-Fixture"] != "retained" {
		t.Fatalf("credential was not rebound to the Control Plane account: %#v %v", reopened, err)
	}
	newKey := "llm-traces/" + accountID + "/trace-1/artifact-1.json"
	loaded, reference, err := garage.Get(ctx, newKey)
	if err != nil || string(loaded) != string(content) || reference.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("account artifact was not retained: %q %#v %v", loaded, reference, err)
	}
	if _, _, err := garage.Get(ctx, objectKey); err == nil {
		t.Fatal("legacy owner artifact prefix remained after successful migration")
	}
	var preserved [9]int
	if err := database.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM llm_profiles WHERE owner_id=$1),
		(SELECT count(*) FROM llm_client_state WHERE owner_id=$1),
		(SELECT count(*) FROM llm_runs WHERE owner_id=$1),
		(SELECT count(*) FROM llm_traces WHERE owner_id=$1),
		(SELECT count(*) FROM llm_trace_observations WHERE owner_id=$1),
		(SELECT count(*) FROM llm_artifacts WHERE owner_id=$1),
		(SELECT count(*) FROM llm_operation_cache WHERE owner_id=$1),
		(SELECT count(*) FROM llm_artifact_delete_batches WHERE owner_id=$1),
		(SELECT count(*) FROM llm_artifact_operations WHERE owner_id=$1)`, accountID).Scan(
		&preserved[0], &preserved[1], &preserved[2], &preserved[3], &preserved[4], &preserved[5], &preserved[6], &preserved[7], &preserved[8]); err != nil {
		t.Fatal(err)
	}
	for index, count := range preserved {
		if count != 1 {
			t.Fatalf("migrated resource category %d has %d rows, want 1", index, count)
		}
	}
	var localIdentityTablesRemain bool
	if err := database.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL OR to_regclass('public.user_sessions') IS NOT NULL`).Scan(&localIdentityTablesRemain); err != nil || localIdentityTablesRemain {
		t.Fatalf("local login tables remain=%v err=%v", localIdentityTablesRemain, err)
	}
}
