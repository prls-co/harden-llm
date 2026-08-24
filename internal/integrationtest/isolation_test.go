//go:build integration

package integrationtest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-042 TEST-053

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSharedServiceIsolation(t *testing.T) {
	if os.Getenv("HARDEN_LLM_TEST_ISOLATION_CHILD") == "1" {
		_, _ = PostgresLease(t)
		_, _ = GarageLease(t)
		os.Exit(17)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	postgresA, dsnA := PostgresLease(t)
	postgresB, dsnB := PostgresLease(t)
	garageA, fixtureA := GarageLease(t)
	garageB, fixtureB := GarageLease(t)

	if postgresA.Endpoint != postgresB.Endpoint || garageA.Endpoint != garageB.Endpoint {
		t.Fatalf("leases did not use the shared service endpoints: postgres=%q/%q garage=%q/%q", postgresA.Endpoint, postgresB.Endpoint, garageA.Endpoint, garageB.Endpoint)
	}
	if fixtureA.Namespace == "" || fixtureA.Namespace == fixtureB.Namespace {
		t.Fatalf("Garage namespaces are not unique: %q %q", fixtureA.Namespace, fixtureB.Namespace)
	}

	connectionA, err := pgx.Connect(ctx, dsnA)
	if err != nil {
		t.Fatal(err)
	}
	defer connectionA.Close(ctx)
	connectionB, err := pgx.Connect(ctx, dsnB)
	if err != nil {
		t.Fatal(err)
	}
	defer connectionB.Close(ctx)
	if _, err := connectionA.Exec(ctx, "CREATE TABLE isolation_sentinel (value text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectionA.Exec(ctx, "INSERT INTO isolation_sentinel (value) VALUES ('database-a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectionB.Exec(ctx, "CREATE TABLE isolation_sentinel (value text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectionB.Exec(ctx, "INSERT INTO isolation_sentinel (value) VALUES ('database-b')"); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := connectionA.QueryRow(ctx, "SELECT value FROM isolation_sentinel").Scan(&value); err != nil || value != "database-a" {
		t.Fatalf("database A sentinel = %q, %v", value, err)
	}
	if err := connectionB.QueryRow(ctx, "SELECT value FROM isolation_sentinel").Scan(&value); err != nil || value != "database-b" {
		t.Fatalf("database B sentinel = %q, %v", value, err)
	}

	if err := PutGarageObject(ctx, fixtureA, "sentinel/a.json", []byte(`{"owner":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if err := PutGarageObject(ctx, fixtureB, "sentinel/b.json", []byte(`{"owner":"b"}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := GetGarageObject(ctx, fixtureA, "sentinel/a.json"); err != nil || string(got) != `{"owner":"a"}` {
		t.Fatalf("Garage A sentinel = %q, %v", got, err)
	}
	if got, err := GetGarageObject(ctx, fixtureB, "sentinel/b.json"); err != nil || string(got) != `{"owner":"b"}` {
		t.Fatalf("Garage B sentinel = %q, %v", got, err)
	}
	if _, err := GetGarageObject(ctx, fixtureA, fixtureB.Key("sentinel/b.json")); err == nil {
		t.Fatal("Garage A read crossed into Garage B namespace")
	}
	if err := DeleteGarageObject(ctx, fixtureA, fixtureB.Key("sentinel/b.json")); err == nil {
		t.Fatal("Garage A delete crossed into Garage B namespace")
	}
	keysA, err := ListGarageObjects(ctx, fixtureA, "sentinel/")
	if err != nil || len(keysA) != 1 || keysA[0] != fixtureA.Key("sentinel/a.json") {
		t.Fatalf("Garage A list = %#v, %v", keysA, err)
	}
	keysB, err := ListGarageObjects(ctx, fixtureB, "sentinel/")
	if err != nil || len(keysB) != 1 || keysB[0] != fixtureB.Key("sentinel/b.json") {
		t.Fatalf("Garage B list = %#v, %v", keysB, err)
	}

	if err := postgresA.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixtureA.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if DatabaseExists(ctx, postgresA.Endpoint, postgresA.Database) {
		t.Fatalf("released Postgres database %q remains", postgresA.Database)
	}
	if keys, err := ListGarageObjects(ctx, fixtureA, ""); err == nil && len(keys) != 0 {
		t.Fatalf("released Garage namespace remains: %#v", keys)
	}
	if !DatabaseExists(ctx, postgresB.Endpoint, postgresB.Database) {
		t.Fatalf("active Postgres database %q disappeared with another lease", postgresB.Database)
	}
	keysB, err = ListGarageObjects(ctx, fixtureB, "sentinel/")
	if err != nil || len(keysB) != 1 {
		t.Fatalf("active Garage namespace changed after another release: %#v %v", keysB, err)
	}

	child := exec.Command(os.Args[0], "-test.run=TestSharedServiceIsolation/child", "-test.v")
	child.Env = append(os.Environ(), "HARDEN_LLM_TEST_ISOLATION_CHILD=1")
	if err := child.Run(); err == nil {
		t.Fatal("failure child unexpectedly passed")
	} else {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 17 {
			t.Fatalf("failure child error = %v", err)
		}
	}
	if err := postgresB.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixtureB.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if DatabaseExists(ctx, postgresB.Endpoint, postgresB.Database) {
		t.Fatalf("final Postgres database %q remains", postgresB.Database)
	}
}
