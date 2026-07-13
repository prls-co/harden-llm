package postgres

import (
	"encoding/json"
	"time"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID          string
	OwnerID     string
	TokenDigest []byte
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

type ProfileRecord struct {
	OwnerID      string
	ID           string
	CredentialID string
	Document     json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CredentialRecord struct {
	OwnerID    string
	ID         string
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
	Origin     string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ClientState struct {
	OwnerID   string
	Document  json.RawMessage
	UpdatedAt time.Time
}

type RunRecord struct {
	OwnerID     string
	ID          string
	ProfileID   string
	TraceID     string
	Status      string
	Request     json.RawMessage
	Result      json.RawMessage
	StartedAt   time.Time
	CompletedAt time.Time
}

type TraceRecord struct {
	OwnerID   string
	TraceID   string
	Record    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ObservationRecord struct {
	OwnerID   string
	TraceID   string
	Sequence  int
	Type      string
	Data      json.RawMessage
	CreatedAt time.Time
}

type ArtifactRecord struct {
	OwnerID     string
	TraceID     string
	ID          string
	Kind        string
	ObjectKey   string
	ContentType string
	SHA256      string
	SizeBytes   int64
	Available   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CacheRecord struct {
	OwnerID       string
	Version       string
	OperationHash string
	Operation     json.RawMessage
	Result        json.RawMessage
	Usage         json.RawMessage
	Cost          json.RawMessage
	Envelope      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type StatsRecord struct {
	OwnerID   string
	Scope     string
	Totals    json.RawMessage
	UpdatedAt time.Time
}
