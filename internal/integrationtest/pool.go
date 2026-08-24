//go:build integration

package integrationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jackc/pgx/v5"
)

const (
	testPoolEnabledEnv      = "HARDEN_LLM_TEST_POOL"
	testRunIDEnv            = "HARDEN_LLM_TEST_RUN_ID"
	testPostgresEndpointEnv = "HARDEN_LLM_TEST_POSTGRES_ENDPOINT"
	testGarageEndpointEnv   = "HARDEN_LLM_TEST_GARAGE_ENDPOINT"
	testPostgresUser        = "harden_test"
	testPostgresPassword    = "harden_test_password"
	testPostgresDefaultDB   = "harden_llm_test"
	testGarageBucket        = "harden-llm-artifacts-test"
	testGarageRegion        = "garage"
	testGarageAccessKey     = "GK00000000000000000000000000000000"
	testGarageSecretKey     = "0000000000000000000000000000000000000000000000000000000000000000"
	leaseOperationTimeout   = 30 * time.Second
	leaseCleanupTimeout     = 30 * time.Second
)

type leaseState struct {
	once sync.Once
	fn   func(context.Context) error
	err  error
}

func newLeaseState(fn func(context.Context) error) *leaseState {
	return &leaseState{fn: fn}
}

// Release is idempotent and is used by tests that need to prove one lease can
// be removed while another lease remains active. The registered t.Cleanup
// callback calls the same operation on every exit path.
func (service *Service) Release(ctx context.Context) error {
	if service == nil || service.release == nil {
		return nil
	}
	service.release.once.Do(func() { service.release.err = service.release.fn(ctx) })
	return service.release.err
}

// Release removes one Garage namespace and is idempotent.
func (fixture Garage) Release(ctx context.Context) error {
	if fixture.release == nil {
		return nil
	}
	fixture.release.once.Do(func() { fixture.release.err = fixture.release.fn(ctx) })
	return fixture.release.err
}

// Key returns the fully leased object key for a relative test key.
func (fixture Garage) Key(relative string) string {
	return fixture.Namespace + relative
}

// Scope returns a fully leased production adapter scope.
func (fixture Garage) Scope(relative string) string {
	return fixture.Namespace + relative
}

// PostgresLease allocates a unique database in the runner-owned Postgres
// service. It intentionally has no per-test Compose fallback: integration
// tests must run through the service-pool task so the lifecycle owner remains
// the canonical runner.
func PostgresLease(t testingTB) (*Service, string) {
	t.Helper()
	endpoint := requirePoolEndpoint(t, testPostgresEndpointEnv)
	if !poolEnabled() {
		t.Fatalf("PostgresLease requires the runner-owned integration service pool")
	}
	database := "harden_test_" + randomLeaseToken(t)
	adminDSN := postgresDSN(endpoint, "postgres")
	ctx, cancel := context.WithTimeout(context.Background(), leaseOperationTimeout)
	defer cancel()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect Postgres service: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(database)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create leased database %q: %v", database, err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close Postgres admin connection: %v", err)
	}
	service := &Service{Endpoint: endpoint, Database: database}
	service.release = newLeaseState(func(releaseContext context.Context) error {
		if releaseContext == nil {
			releaseContext = context.Background()
		}
		bounded, releaseCancel := context.WithTimeout(releaseContext, leaseCleanupTimeout)
		defer releaseCancel()
		connection, connectErr := pgx.Connect(bounded, adminDSN)
		if connectErr != nil {
			return fmt.Errorf("connect Postgres cleanup: %w", connectErr)
		}
		defer connection.Close(bounded)
		if _, dropErr := connection.Exec(bounded, "DROP DATABASE IF EXISTS "+quoteIdentifier(database)+" WITH (FORCE)"); dropErr != nil {
			return fmt.Errorf("drop leased database %q: %w", database, dropErr)
		}
		return nil
	})
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), leaseCleanupTimeout)
		defer cleanupCancel()
		if err := service.Release(cleanupContext); err != nil {
			t.Errorf("Postgres lease cleanup %q: %v", database, err)
		}
	})
	dsn := postgresDSN(endpoint, database)
	waitPostgres(t, endpoint, dsn)
	return service, dsn
}

// GarageLease allocates a cryptographically unique key-prefix lease in the
// runner-owned Garage service. All operations exposed below prepend this
// namespace and reject already-qualified or escaping keys.
func GarageLease(t testingTB) (*Service, Garage) {
	t.Helper()
	endpoint := "http://" + requirePoolEndpoint(t, testGarageEndpointEnv)
	if !poolEnabled() {
		t.Fatalf("GarageLease requires the runner-owned integration service pool")
	}
	runID := sanitizeLeasePart(getenv(testRunIDEnv, "run"))
	namespace := fmt.Sprintf("harden-llm-test/%s/%s/", runID, randomLeaseToken(t))
	fixture := Garage{
		Endpoint: endpoint, Bucket: testGarageBucket, Region: testGarageRegion,
		AccessKeyID: testGarageAccessKey, SecretAccessKey: testGarageSecretKey,
		Namespace: namespace,
	}
	fixture.release = newLeaseState(func(releaseContext context.Context) error {
		return deleteGaragePrefix(releaseContext, fixture)
	})
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), leaseCleanupTimeout)
		defer cleanupCancel()
		if err := fixture.Release(cleanupContext); err != nil {
			t.Errorf("Garage lease cleanup %q: %v", namespace, err)
		}
	})
	return &Service{Endpoint: endpoint}, fixture
}

// PutGarageObject writes a JSON-independent sentinel through the lease API.
// Production artifact tests still use the real artifacts adapter; these small
// helpers make the storage namespace contract directly observable.
func PutGarageObject(ctx context.Context, fixture Garage, relative string, content []byte) error {
	key, err := fixtureKey(fixture, relative, false)
	if err != nil {
		return err
	}
	client := newGarageClient(fixture)
	requestContext, cancel := context.WithTimeout(ctx, leaseOperationTimeout)
	defer cancel()
	_, err = client.PutObject(requestContext, &s3.PutObjectInput{
		Bucket: aws.String(fixture.Bucket), Key: aws.String(key), Body: strings.NewReader(string(content)),
		ContentLength: aws.Int64(int64(len(content))), ContentType: aws.String("application/json"),
	})
	return err
}

// GetGarageObject reads one relative leased object.
func GetGarageObject(ctx context.Context, fixture Garage, relative string) ([]byte, error) {
	key, err := fixtureKey(fixture, relative, false)
	if err != nil {
		return nil, err
	}
	client := newGarageClient(fixture)
	requestContext, cancel := context.WithTimeout(ctx, leaseOperationTimeout)
	defer cancel()
	output, err := client.GetObject(requestContext, &s3.GetObjectInput{Bucket: aws.String(fixture.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer output.Body.Close()
	return io.ReadAll(output.Body)
}

// DeleteGarageObject deletes one relative leased object.
func DeleteGarageObject(ctx context.Context, fixture Garage, relative string) error {
	key, err := fixtureKey(fixture, relative, false)
	if err != nil {
		return err
	}
	client := newGarageClient(fixture)
	requestContext, cancel := context.WithTimeout(ctx, leaseOperationTimeout)
	defer cancel()
	_, err = client.DeleteObject(requestContext, &s3.DeleteObjectInput{Bucket: aws.String(fixture.Bucket), Key: aws.String(key)})
	return err
}

// ListGarageObjects lists only objects below one relative lease prefix.
func ListGarageObjects(ctx context.Context, fixture Garage, relativePrefix string) ([]string, error) {
	keyPrefix, err := fixtureKey(fixture, relativePrefix, true)
	if err != nil {
		return nil, err
	}
	return listGarageKeys(ctx, fixture, keyPrefix)
}

// DatabaseExists reports the exact lease inventory without failing the caller
// on a transient cleanup connection error.
func DatabaseExists(ctx context.Context, endpoint, database string) bool {
	if !validLeaseName(database) {
		return false
	}
	connection, err := pgx.Connect(ctx, postgresDSN(endpoint, "postgres"))
	if err != nil {
		return false
	}
	defer connection.Close(ctx)
	var exists bool
	if err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", database).Scan(&exists); err != nil {
		return false
	}
	return exists
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
	Errorf(string, ...any)
}

func poolEnabled() bool {
	return getenv(testPoolEnabledEnv, "") == "1"
}

func requirePoolEndpoint(t testingTB, name string) string {
	value := strings.TrimSpace(getenv(name, ""))
	if value == "" {
		t.Fatalf("missing runner-owned integration endpoint %s", name)
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		t.Fatalf("invalid runner-owned integration endpoint %s", name)
	}
	return value
}

func postgresDSN(endpoint, database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", testPostgresUser, url.PathEscape(testPostgresPassword), endpoint, database)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func validLeaseName(value string) bool {
	if len(value) < 12 || len(value) > 63 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return strings.HasPrefix(value, "harden_test_")
}

func fixtureKey(fixture Garage, relative string, prefixOnly bool) (string, error) {
	if fixture.Namespace == "" {
		return "", errors.New("Garage lease namespace is required")
	}
	cleanCandidate := relative
	if prefixOnly {
		cleanCandidate = strings.TrimSuffix(cleanCandidate, "/")
	}
	if strings.HasPrefix(relative, fixture.Namespace) || strings.HasPrefix(relative, "harden-llm-test/") || strings.HasPrefix(relative, "/") || path.IsAbs(relative) || (cleanCandidate != "" && path.Clean(cleanCandidate) != cleanCandidate) || strings.Contains(relative, "//") || strings.Contains(relative, "\\") || strings.Contains(relative, "\x00") {
		return "", errors.New("object escapes Garage lease namespace")
	}
	if !prefixOnly && strings.TrimSpace(relative) == "" {
		return "", errors.New("object key is required")
	}
	return fixture.Key(relative), nil
}

func newGarageClient(fixture Garage) *s3.Client {
	credentials := aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: fixture.AccessKeyID, SecretAccessKey: fixture.SecretAccessKey, Source: "harden-llm-test"}, nil
	}))
	config := aws.Config{Region: fixture.Region, Credentials: credentials}
	return s3.NewFromConfig(config, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(fixture.Endpoint)
		options.UsePathStyle = true
		options.DisableMultiRegionAccessPoints = true
	})
}

func listGarageKeys(ctx context.Context, fixture Garage, prefix string) ([]string, error) {
	client := newGarageClient(fixture)
	requestContext, cancel := context.WithTimeout(ctx, leaseOperationTimeout)
	defer cancel()
	keys := []string{}
	var token *string
	for {
		output, err := client.ListObjectsV2(requestContext, &s3.ListObjectsV2Input{Bucket: aws.String(fixture.Bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, object := range output.Contents {
			if object.Key != nil {
				keys = append(keys, aws.ToString(object.Key))
			}
		}
		if !aws.ToBool(output.IsTruncated) || output.NextContinuationToken == nil {
			break
		}
		token = output.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}

func deleteGaragePrefix(ctx context.Context, fixture Garage) error {
	if fixture.Namespace == "" {
		return nil
	}
	keys, err := listGarageKeys(ctx, fixture, fixture.Namespace)
	if err != nil {
		return fmt.Errorf("list Garage lease %q: %w", fixture.Namespace, err)
	}
	client := newGarageClient(fixture)
	requestContext, cancel := context.WithTimeout(ctx, leaseCleanupTimeout)
	defer cancel()
	for start := 0; start < len(keys); start += 1000 {
		end := start + 1000
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]types.ObjectIdentifier, 0, end-start)
		for _, key := range keys[start:end] {
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(key)})
		}
		if _, err := client.DeleteObjects(requestContext, &s3.DeleteObjectsInput{Bucket: aws.String(fixture.Bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}}); err != nil {
			return fmt.Errorf("delete Garage lease %q: %w", fixture.Namespace, err)
		}
	}
	remaining, err := listGarageKeys(requestContext, fixture, fixture.Namespace)
	if err != nil {
		return fmt.Errorf("verify Garage lease cleanup %q: %w", fixture.Namespace, err)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("Garage lease %q retained %d objects", fixture.Namespace, len(remaining))
	}
	return nil
}

func randomLeaseToken(t testingTB) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate integration lease token: %v", err)
	}
	return hex.EncodeToString(value)
}

func sanitizeLeasePart(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			builder.WriteRune(character)
		}
	}
	if builder.Len() == 0 {
		return "run"
	}
	return builder.String()
}

func getenv(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
