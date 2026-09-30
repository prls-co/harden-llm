package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LocalIdentityOwners returns the legacy user IDs and whether that table still
// exists. New databases remove it after the one-time account rehome.
func (store *Store) LocalIdentityOwners(ctx context.Context) ([]string, bool, error) {
	var present bool
	if err := store.pool.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&present); err != nil {
		return nil, false, fmt.Errorf("postgres: inspect local identity table: %w", err)
	}
	if !present {
		return nil, false, nil
	}
	rows, err := store.pool.Query(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: list local identity owners: %w", err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, false, fmt.Errorf("postgres: scan local identity owner: %w", err)
		}
		owners = append(owners, owner)
	}
	return owners, true, rows.Err()
}

// RehomeOwnerIDs atomically updates every product owner reference and replaces
// each provider credential ciphertext prepared with the target account AAD.
func (store *Store) RehomeOwnerIDs(ctx context.Context, mapping map[string]string, credentials map[string][]CredentialRecord) error {
	if store == nil || store.pool == nil || len(mapping) == 0 || len(mapping) != len(credentials) {
		return errors.New("postgres: owner mapping and credential rows are required")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin owner rehome: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLock); err != nil {
		return fmt.Errorf("postgres: lock owner rehome: %w", err)
	}
	rows, err := transaction.Query(ctx, `SELECT id FROM users ORDER BY id FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("postgres: lock local identity owners: %w", err)
	}
	owners := make([]string, 0, len(mapping))
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: scan local identity owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("postgres: list local identity owners: %w", err)
	}
	rows.Close()
	if len(owners) != len(mapping) {
		return errors.New("postgres: owner mapping must name every local identity exactly once")
	}
	for _, owner := range owners {
		if _, ok := mapping[owner]; !ok {
			return errors.New("postgres: owner mapping must name every local identity exactly once")
		}
	}

	sources := make(map[string]struct{}, len(mapping))
	targets := make(map[string]string, len(mapping))
	for source, target := range mapping {
		if err := validateIdentifier("legacy owner ID", source); err != nil {
			return err
		}
		if err := validateIdentifier("Control Plane account ID", target); err != nil {
			return err
		}
		if previous, exists := targets[target]; exists {
			return fmt.Errorf("postgres: identities %q and %q cannot share one account", previous, source)
		}
		sources[source] = struct{}{}
		targets[target] = source
	}
	for source, target := range mapping {
		if _, collision := sources[target]; collision && target != source {
			return errors.New("postgres: account mapping cannot reuse another local identity ID")
		}
		var occupied bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=$1)`, target).Scan(&occupied); err != nil {
			return fmt.Errorf("postgres: inspect target account: %w", err)
		}
		if occupied && target != source {
			return errors.New("postgres: target account ID already belongs to another local identity")
		}
	}

	if len(credentials) != len(mapping) {
		return errors.New("postgres: re-encrypted credential set is incomplete")
	}
	for _, source := range owners {
		target := mapping[source]
		provided, ok := credentials[source]
		if !ok {
			return errors.New("postgres: re-encrypted credential set is incomplete")
		}
		currentRows, err := transaction.Query(ctx, `
			SELECT owner_id, credential_id, key_id, nonce, ciphertext, normalized_origin, metadata, created_at
			FROM llm_endpoint_credentials WHERE owner_id=$1 ORDER BY credential_id FOR UPDATE`, source)
		if err != nil {
			return fmt.Errorf("postgres: inspect owner credentials: %w", err)
		}
		current := make([]CredentialRecord, 0, len(provided))
		for currentRows.Next() {
			var credential CredentialRecord
			if err := currentRows.Scan(&credential.OwnerID, &credential.ID, &credential.KeyID, &credential.Nonce, &credential.Ciphertext,
				&credential.Origin, &credential.Metadata, &credential.CreatedAt); err != nil {
				currentRows.Close()
				return fmt.Errorf("postgres: scan owner credential: %w", err)
			}
			current = append(current, credential)
		}
		if err := currentRows.Err(); err != nil {
			currentRows.Close()
			return fmt.Errorf("postgres: list owner credentials: %w", err)
		}
		currentRows.Close()
		if len(current) != len(provided) {
			return errors.New("postgres: re-encrypted credential set does not match stored credentials")
		}
		byID := make(map[string]CredentialRecord, len(provided))
		for _, credential := range provided {
			if credential.OwnerID != target || credential.ID == "" || credential.KeyID == "" || len(credential.Nonce) != 12 || len(credential.Ciphertext) < 16 {
				return errors.New("postgres: re-encrypted credential is invalid")
			}
			if _, duplicate := byID[credential.ID]; duplicate {
				return errors.New("postgres: re-encrypted credential set contains duplicates")
			}
			byID[credential.ID] = credential
		}
		for _, previous := range current {
			credential, ok := byID[previous.ID]
			if !ok || credential.Origin != previous.Origin || !bytes.Equal(credential.Metadata, previous.Metadata) || !credential.CreatedAt.Equal(previous.CreatedAt) {
				return errors.New("postgres: re-encrypted credential binding does not match stored credentials")
			}
			if _, err := transaction.Exec(ctx, `UPDATE llm_endpoint_credentials
				SET key_id=$3, nonce=$4, ciphertext=$5, updated_at=GREATEST(updated_at, now())
				WHERE owner_id=$1 AND credential_id=$2`, source, credential.ID, credential.KeyID, credential.Nonce, credential.Ciphertext); err != nil {
				return fmt.Errorf("postgres: replace owner credential ciphertext: %w", err)
			}
		}
	}

	for _, source := range owners {
		target := mapping[source]
		if source != target {
			if _, err := transaction.Exec(ctx, `UPDATE users SET id=$2 WHERE id=$1`, source, target); err != nil {
				return fmt.Errorf("postgres: rehome product account: %w", err)
			}
		}
		oldPrefix, newPrefix := artifactOwnerPrefix(source), artifactOwnerPrefix(target)
		for _, table := range []string{"llm_artifacts", "llm_artifact_operations"} {
			query := fmt.Sprintf(`UPDATE %s SET object_key=$1 || substr(object_key,length($2)+1)
				WHERE owner_id=$3 AND left(object_key,length($2))=$2`, table)
			if _, err := transaction.Exec(ctx, query, newPrefix, oldPrefix, target); err != nil {
				return fmt.Errorf("postgres: rehome %s object references: %w", table, err)
			}
		}
	}
	if err := validateNoLegacyReferences(ctx, transaction, mapping); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit owner rehome: %w", err)
	}
	return nil
}

// ValidateNoLegacyReferences checks the owner and artifact-key columns before
// the old identity tables or object prefixes are retired.
func (store *Store) ValidateNoLegacyReferences(ctx context.Context, mapping map[string]string) error {
	return validateNoLegacyReferences(ctx, store.pool, mapping)
}

func validateNoLegacyReferences(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, mapping map[string]string) error {
	ownerTables := []string{
		"user_sessions", "llm_endpoint_credentials", "llm_profiles", "llm_client_state", "llm_runs", "llm_traces",
		"llm_trace_observations", "llm_artifacts", "llm_operation_cache", "llm_artifact_delete_batches", "llm_artifact_operations",
	}
	for source, target := range mapping {
		if source == target {
			continue
		}
		for _, table := range ownerTables {
			var present, found bool
			if err := queryer.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&present); err != nil {
				return fmt.Errorf("postgres: inspect %s during owner rehome: %w", table, err)
			}
			if !present {
				continue
			}
			query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE owner_id=$1)`, table)
			if err := queryer.QueryRow(ctx, query, source).Scan(&found); err != nil {
				return fmt.Errorf("postgres: verify %s owner rehome: %w", table, err)
			}
			if found {
				return fmt.Errorf("postgres: legacy owner references remain in %s", table)
			}
		}
		for _, table := range []string{"llm_artifacts", "llm_artifact_operations"} {
			var present, found bool
			if err := queryer.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&present); err != nil {
				return fmt.Errorf("postgres: inspect %s during object rehome: %w", table, err)
			}
			if !present {
				continue
			}
			query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE position('/' || $1 || '/' in object_key) > 0)`, table)
			if err := queryer.QueryRow(ctx, query, source).Scan(&found); err != nil {
				return fmt.Errorf("postgres: verify %s object rehome: %w", table, err)
			}
			if found {
				return fmt.Errorf("postgres: legacy artifact references remain in %s", table)
			}
		}
	}
	return nil
}

func artifactOwnerPrefix(ownerID string) string { return "llm-traces/" + ownerID + "/" }
