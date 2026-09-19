package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-server/server/policy"
)

// postgresDSNEnv names a PostgreSQL URL; when set, the backend tests below
// also run against PostgreSQL, each in a fresh schema.
const postgresDSNEnv = "GHOST_TEST_POSTGRES_DSN"

// backend is a database the store can open, empty and unmigrated.
type backend struct {
	driver string
	dsn    string
	// raw opens the database without migrating it.
	raw func(t *testing.T) *sql.DB
}

func backends(t *testing.T) []backend {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.db")
	out := []backend{{
		driver: string(SQLite), dsn: path,
		raw: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			return db
		},
	}}
	if base := os.Getenv(postgresDSNEnv); base != "" {
		out = append(out, postgresBackend(t, base))
	}
	return out
}

// postgresBackend creates a throwaway schema and returns a DSN whose
// search_path points at it.
func postgresBackend(t *testing.T, base string) backend {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	schema := "ghost_test_" + hex.EncodeToString(b)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn := u.String()
	return backend{
		driver: string(Postgres), dsn: dsn,
		raw: func(t *testing.T) *sql.DB {
			t.Helper()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			return db
		},
	}
}

func TestNetworkInteractiveEnrollment(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.driver, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, b.driver, b.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			now := time.Now().UTC().Truncate(time.Millisecond)
			for _, n := range []Network{
				{Name: "open", Pool: "100.64.0.0/10", InteractiveEnrollment: true, Policy: policy.Default(), CreatedAt: now},
				{Name: "closed", Pool: "100.64.0.0/10", Policy: policy.Default(), CreatedAt: now},
			} {
				if err := s.CreateNetwork(ctx, n); err != nil {
					t.Fatal(err)
				}
			}
			tests := []struct {
				name string
				want bool
			}{{"open", true}, {"closed", false}}
			for _, tt := range tests {
				n, err := s.GetNetwork(ctx, tt.name)
				if err != nil || n.InteractiveEnrollment != tt.want {
					t.Fatalf("%s: %v interactive_enrollment=%v, want %v", tt.name, err, n.InteractiveEnrollment, tt.want)
				}
			}

			// Toggling the switch leaves the policy revision alone.
			on := true
			n, err := s.UpdateNetwork(ctx, "closed", NetworkUpdate{InteractiveEnrollment: &on})
			if err != nil || !n.InteractiveEnrollment || n.PolicyRevision != 0 || n.Isolation != policy.IsolationNone {
				t.Fatalf("enable: %v %+v", err, n)
			}
			// Isolation and the switch change together, atomically.
			off, hubOnly := false, policy.IsolationHubOnly
			n, err = s.UpdateNetwork(ctx, "closed", NetworkUpdate{Isolation: &hubOnly, InteractiveEnrollment: &off})
			if err != nil || n.InteractiveEnrollment || n.Isolation != policy.IsolationHubOnly || n.PolicyRevision != 1 {
				t.Fatalf("disable with isolation: %v %+v", err, n)
			}
			// A policy change keeps the switch.
			if n, err = s.SetNetworkPolicy(ctx, "closed", policy.Default()); err != nil || n.InteractiveEnrollment || n.PolicyRevision != 2 {
				t.Fatalf("policy: %v %+v", err, n)
			}
			ns, err := s.ListNetworks(ctx)
			if err != nil || len(ns) != 2 || ns[0].Name != "closed" || ns[0].InteractiveEnrollment || !ns[1].InteractiveEnrollment {
				t.Fatalf("list: %v %+v", err, ns)
			}
			if _, err := s.UpdateNetwork(ctx, "missing", NetworkUpdate{InteractiveEnrollment: &on}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing network: %v", err)
			}
		})
	}
}

// TestMigrationEnablesInteractiveEnrollment upgrades a database at schema
// version 1: networks that predate the switch keep interactive enrolment on.
func TestMigrationEnablesInteractiveEnrollment(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.driver, func(t *testing.T) {
			ctx := context.Background()
			db := b.raw(t)
			body, err := migrationFS.ReadFile("migrations/0001_init.sql")
			if err != nil {
				t.Fatal(err)
			}
			stmts := append(splitStatements(string(body)),
				`CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY, applied_at BIGINT NOT NULL)`,
				`INSERT INTO schema_migrations (version, applied_at) VALUES (1, 0)`,
				`INSERT INTO networks (name, pool, isolation, policy, policy_revision, created_at)
					VALUES ('legacy', '100.64.0.0/10', 'none', '{}', 3, 0)`)
			for _, stmt := range stmts {
				if _, err := db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("v1 schema: %v", err)
				}
			}
			_ = db.Close()

			s, err := Open(ctx, b.driver, b.dsn)
			if err != nil {
				t.Fatalf("migrate: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			n, err := s.GetNetwork(ctx, "legacy")
			if err != nil || !n.InteractiveEnrollment || n.PolicyRevision != 3 {
				t.Fatalf("legacy network: %v %+v", err, n)
			}
			// A network created without the column still gets the default.
			if _, err := s.db.ExecContext(ctx, `INSERT INTO networks (name, pool, isolation, policy, policy_revision, created_at)
				VALUES ('raw', '100.64.0.0/10', 'none', '{}', 0, 0)`); err != nil {
				t.Fatal(err)
			}
			if n, err := s.GetNetwork(ctx, "raw"); err != nil || !n.InteractiveEnrollment {
				t.Fatalf("column default: %v %+v", err, n)
			}
		})
	}
}
