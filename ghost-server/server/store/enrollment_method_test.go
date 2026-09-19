package store

import (
	"context"
	"testing"
	"time"
)

// TestPeerEnrollmentMethod: the method is stored with the peer and kept by
// updates.
func TestPeerEnrollmentMethod(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.driver, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, b.driver, b.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			now := time.Now().UTC().Truncate(time.Millisecond)
			if err := s.CreateNetwork(ctx, Network{Name: "n", Pool: "100.64.0.0/10", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if err := s.CreatePeer(ctx, Peer{ID: "p", TokenHash: "h", Network: "n", AuthKeyID: "key_1",
				EnrollmentMethod: "auth_key", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			p, err := s.UpdatePeer(ctx, "p", func(p *Peer) error { p.Name = "renamed"; return nil })
			if err != nil || p.EnrollmentMethod != "auth_key" || p.AuthKeyID != "key_1" {
				t.Fatalf("update: %v %+v", err, p)
			}
			if p, err = s.GetPeer(ctx, "p"); err != nil || p.EnrollmentMethod != "auth_key" || p.Name != "renamed" {
				t.Fatalf("get: %v %+v", err, p)
			}
		})
	}
}

// TestMigrationBackfillsEnrollmentMethod upgrades a database at schema version
// 2: a pre-auth key id means auth_key, a claimed enrolment naming the peer
// means interactive, and anything else stays unknown.
func TestMigrationBackfillsEnrollmentMethod(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.driver, func(t *testing.T) {
			ctx := context.Background()
			db := b.raw(t)
			var stmts []string
			for _, name := range []string{"0001_init.sql", "0002_network_interactive_enrollment.sql"} {
				body, err := migrationFS.ReadFile("migrations/" + name)
				if err != nil {
					t.Fatal(err)
				}
				stmts = append(stmts, splitStatements(string(body))...)
			}
			stmts = append(stmts,
				`CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY, applied_at BIGINT NOT NULL)`,
				`INSERT INTO schema_migrations (version, applied_at) VALUES (1, 0), (2, 0)`,
				`INSERT INTO networks (name, pool, isolation, policy, policy_revision, created_at)
					VALUES ('n', '100.64.0.0/10', 'none', '{}', 0, 0)`,
				`INSERT INTO peers (id, token_hash, network, auth_key_id, created_at) VALUES ('keyed', 'h1', 'n', 'key_1', 0)`,
				`INSERT INTO peers (id, token_hash, network, created_at) VALUES ('claimed', 'h2', 'n', 0)`,
				`INSERT INTO peers (id, token_hash, network, created_at) VALUES ('unknown', 'h3', 'n', 0)`,
				`INSERT INTO enrollments (code_hash, poll_hash, network, status, peer_id, created_at, expires_at)
					VALUES ('c1', 'p1', 'n', 'claimed', 'claimed', 0, 0)`,
				`INSERT INTO enrollments (code_hash, poll_hash, network, status, created_at, expires_at)
					VALUES ('c2', 'p2', 'n', 'pending', 0, 0)`)
			for _, stmt := range stmts {
				if _, err := db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("v2 schema: %v", err)
				}
			}
			_ = db.Close()

			s, err := Open(ctx, b.driver, b.dsn)
			if err != nil {
				t.Fatalf("migrate: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			tests := []struct{ id, want string }{{"keyed", "auth_key"}, {"claimed", "interactive"}, {"unknown", ""}}
			for _, tt := range tests {
				p, err := s.GetPeer(ctx, tt.id)
				if err != nil || p.EnrollmentMethod != tt.want {
					t.Errorf("%s: %v enrollment_method=%q, want %q", tt.id, err, p.EnrollmentMethod, tt.want)
				}
			}
		})
	}
}
