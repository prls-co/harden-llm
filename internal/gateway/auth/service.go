package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
)

const (
	sessionTokenBytes = 32
	sessionIDBytes    = 16
	minimumSessionTTL = time.Minute
	maximumSessionTTL = 30 * 24 * time.Hour
	// Each Argon2id verification uses 64 MiB. Bounding concurrent logins keeps
	// unauthenticated traffic from creating unbounded memory pressure.
	maximumConcurrentLogins = 4
)

// ErrUnauthenticated deliberately does not distinguish invalid auth states.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

type Config struct {
	Store      *postgres.Store
	SessionTTL time.Duration
	Clock      func() time.Time
	Random     io.Reader
}

type Service struct {
	store      *postgres.Store
	sessionTTL time.Duration
	clock      func() time.Time
	random     io.Reader
	loginSlots chan struct{}
}

type Principal struct {
	OwnerID   string    `json:"ownerId"`
	Email     string    `json:"email"`
	SessionID string    `json:"sessionId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type LoginResult struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Principal   Principal `json:"principal"`
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("auth: Postgres store is required")
	}
	if config.SessionTTL < minimumSessionTTL || config.SessionTTL > maximumSessionTTL {
		return nil, errors.New("auth: session TTL is outside the supported range")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Service{
		store: config.Store, sessionTTL: config.SessionTTL, clock: config.Clock, random: config.Random,
		loginSlots: make(chan struct{}, maximumConcurrentLogins),
	}, nil
}

func (service *Service) BootstrapUser(ctx context.Context, ownerID, email, password string) (postgres.User, error) {
	if service == nil {
		return postgres.User{}, errors.New("auth: service is not initialized")
	}
	passwordHash, err := hashPassword(password, service.random)
	if err != nil {
		return postgres.User{}, err
	}
	now := service.clock().UTC()
	user := postgres.User{ID: strings.TrimSpace(ownerID), Email: strings.ToLower(strings.TrimSpace(email)), PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	if err := service.store.CreateUser(ctx, user); err != nil {
		return postgres.User{}, fmt.Errorf("auth: bootstrap user: %w", err)
	}
	user.PasswordHash = ""
	return user, nil
}

func (service *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	if service == nil {
		return LoginResult{}, ErrUnauthenticated
	}
	select {
	case service.loginSlots <- struct{}{}:
		defer func() { <-service.loginSlots }()
	case <-ctx.Done():
		return LoginResult{}, fmt.Errorf("auth: wait for password verifier: %w", ctx.Err())
	}
	user, err := service.store.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			burnPassword(password)
			return LoginResult{}, ErrUnauthenticated
		}
		return LoginResult{}, fmt.Errorf("auth: load user: %w", err)
	}
	if !verifyPassword(user.PasswordHash, password) {
		return LoginResult{}, ErrUnauthenticated
	}
	tokenBytes := make([]byte, sessionTokenBytes)
	sessionIDBytesValue := make([]byte, sessionIDBytes)
	if _, err := io.ReadFull(service.random, tokenBytes); err != nil {
		return LoginResult{}, errors.New("auth: session token generation failed")
	}
	if _, err := io.ReadFull(service.random, sessionIDBytesValue); err != nil {
		return LoginResult{}, errors.New("auth: session identity generation failed")
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	now := service.clock().UTC()
	session := postgres.Session{
		ID: base64.RawURLEncoding.EncodeToString(sessionIDBytesValue), OwnerID: user.ID,
		TokenDigest: digest[:], ExpiresAt: now.Add(service.sessionTTL), CreatedAt: now,
	}
	if err := service.store.CreateSession(ctx, session); err != nil {
		return LoginResult{}, fmt.Errorf("auth: persist session: %w", err)
	}
	principal := Principal{OwnerID: user.ID, Email: user.Email, SessionID: session.ID, ExpiresAt: session.ExpiresAt}
	return LoginResult{AccessToken: token, ExpiresAt: session.ExpiresAt, Principal: principal}, nil
}

func (service *Service) AuthenticateHeader(ctx context.Context, authorizationValues []string) (Principal, error) {
	if len(authorizationValues) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	value := authorizationValues[0]
	if !strings.HasPrefix(value, "Bearer ") || strings.Count(value, " ") != 1 {
		return Principal{}, ErrUnauthenticated
	}
	return service.AuthenticateToken(ctx, strings.TrimPrefix(value, "Bearer "))
}

func (service *Service) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	if service == nil || !validToken(token) {
		return Principal{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(token))
	session, err := service.store.SessionByDigest(ctx, digest[:])
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, fmt.Errorf("auth: load session: %w", err)
	}
	now := service.clock().UTC()
	if session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
		return Principal{}, ErrUnauthenticated
	}
	user, err := service.store.UserByID(ctx, session.OwnerID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, fmt.Errorf("auth: load session owner: %w", err)
	}
	return Principal{OwnerID: user.ID, Email: user.Email, SessionID: session.ID, ExpiresAt: session.ExpiresAt}, nil
}

func (service *Service) Logout(ctx context.Context, token string) error {
	if service == nil || !validToken(token) {
		return ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(token))
	session, err := service.store.SessionByDigest(ctx, digest[:])
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return ErrUnauthenticated
		}
		return fmt.Errorf("auth: load session for logout: %w", err)
	}
	now := service.clock().UTC()
	if session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
		return ErrUnauthenticated
	}
	if err := service.store.RevokeSession(ctx, session.OwnerID, digest[:], now); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return ErrUnauthenticated
		}
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

// LogoutPrincipal revokes an authenticated session by its safe owner/session
// identity, allowing middleware to discard the bearer token before handlers.
func (service *Service) LogoutPrincipal(ctx context.Context, principal Principal) error {
	if service == nil || principal.OwnerID == "" || principal.SessionID == "" {
		return ErrUnauthenticated
	}
	now := service.clock().UTC()
	if !now.Before(principal.ExpiresAt) {
		return ErrUnauthenticated
	}
	if err := service.store.RevokeSessionByID(ctx, principal.OwnerID, principal.SessionID, now); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return ErrUnauthenticated
		}
		return fmt.Errorf("auth: revoke principal session: %w", err)
	}
	return nil
}

func validToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(sessionTokenBytes) || strings.TrimSpace(token) != token {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(decoded) == sessionTokenBytes && base64.RawURLEncoding.EncodeToString(decoded) == token
}
