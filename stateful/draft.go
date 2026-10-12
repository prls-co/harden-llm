package stateful

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Preserve the reference host's existing seven-day draft lifetime.
const draftLifetime = 7 * 24 * time.Hour

type Draft struct {
	DraftKey string          `json:"draftKey"`
	Revision int64           `json:"revision"`
	Payload  json.RawMessage `json:"payload"`
}
type SaveDraftInput struct {
	Revision int64           `json:"revision"`
	Payload  json.RawMessage `json:"payload"`
}

var persistedScopeOrSecret = regexp.MustCompile(`(?i)^(api[-_]?key|.*api[-_]?key|authorization|password|secret|token|credential|credentials|sessionReference|session|owner|ownerId|userId|apiKeyHash|result|output)$`)

func validateDraftKey(key string) error {
	if key == "" || strings.TrimSpace(key) != key || len(key) > 128 || !utf8.ValidString(key) || strings.ContainsAny(key, "\x00\r\n") {
		return &OperationError{Code: "draft_invalid", Message: "Draft key must contain 1 to 128 UTF-8 bytes."}
	}
	return nil
}
func unsafePersistedField(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if persistedScopeOrSecret.MatchString(key) {
				return true
			}
			if key != "schema" && unsafePersistedField(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if unsafePersistedField(child) {
				return true
			}
		}
	}
	return false
}
func validateDraft(input SaveDraftInput) error {
	if input.Revision < 0 || len(input.Payload) > MaxSnapshotBytes {
		return &OperationError{Code: "draft_invalid", Message: "Draft revision must be nonnegative and payload at most 256 KiB."}
	}
	var data map[string]any
	if decodeStrict(input.Payload, &data) != nil || data == nil || unsafePersistedField(data) {
		return &OperationError{Code: "draft_invalid", Message: "Draft must be an object without scope selectors, credentials or results."}
	}
	return nil
}
func (s *Service) GetDraft(ctx context.Context, hash, key string) (draft Draft, err error) {
	if err = checkScope(hash); err != nil {
		return draft, err
	}
	if err = validateDraftKey(key); err != nil {
		return draft, err
	}
	draft = Draft{DraftKey: key, Payload: json.RawMessage(`{}`)}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT revision,payload FROM drafts WHERE api_key_hash=$1 AND draft_key=$2 AND expires_at>$3", hash, key, s.now().UTC()).Scan(&draft.Revision, &draft.Payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return draft, storageFailure(err)
}
func (s *Service) SaveDraft(ctx context.Context, hash, key string, input SaveDraftInput) (draft Draft, err error) {
	if err = checkScope(hash); err != nil {
		return draft, err
	}
	if err = validateDraftKey(key); err != nil {
		return draft, err
	}
	if err = validateDraft(input); err != nil {
		return draft, err
	}
	now := s.now().UTC()
	payload := bytes.Clone(input.Payload)
	draft = Draft{DraftKey: key, Payload: payload}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM drafts WHERE api_key_hash=$1 AND draft_key IN (SELECT draft_key FROM drafts WHERE api_key_hash=$1 AND expires_at<=$2 ORDER BY expires_at,draft_key LIMIT 100)`, hash, now); err != nil {
			return err
		}
		// An expired row is logically absent even if bounded cleanup did not select it.
		err := tx.QueryRow(ctx, `INSERT INTO drafts(api_key_hash,draft_key,revision,payload,updated_at,expires_at)
   SELECT $1,$2,$6::bigint+1,$3,$4,$5
   WHERE $6=0 OR EXISTS(SELECT 1 FROM drafts WHERE api_key_hash=$1 AND draft_key=$2 AND revision=$6 AND expires_at>$4)
   ON CONFLICT(api_key_hash,draft_key) DO UPDATE SET
    revision=CASE WHEN drafts.expires_at<=EXCLUDED.updated_at THEN 1 ELSE drafts.revision+1 END,
    payload=EXCLUDED.payload,updated_at=EXCLUDED.updated_at,expires_at=EXCLUDED.expires_at
   WHERE ($6=0 AND drafts.expires_at<=EXCLUDED.updated_at) OR ($6>0 AND drafts.revision=$6 AND drafts.expires_at>EXCLUDED.updated_at)
   RETURNING revision`, hash, key, payload, now, now.Add(draftLifetime), input.Revision).Scan(&draft.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return revisionConflict()
		}
		return err
	})
	return draft, storageFailure(err)
}
