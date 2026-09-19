package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/policy"
)

func TestSQLiteStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateNetwork(ctx, Network{Name: "n", Pool: "100.64.0.0/10", Isolation: policy.IsolationNone,
		Policy: policy.Default(), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNetwork(ctx, Network{Name: "n", Pool: "x", Policy: policy.Default(), CreatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate network: %v", err)
	}
	doc := policy.Default()
	doc.Tags["tag:exit"] = policy.TagDef{}
	doc.Exit = []policy.ExitRule{{Name: "web", Target: []string{"tag:exit"}, Allow: []string{"a.example:443"}}}
	n, err := s.SetNetworkPolicy(ctx, "n", doc)
	if err != nil || n.PolicyRevision != 1 || len(n.Policy.Exit) != 1 {
		t.Fatalf("policy: %v %+v", err, n)
	}
	if n, err = s.SetNetworkIsolation(ctx, "n", policy.IsolationHubOnly); err != nil || n.Isolation != policy.IsolationHubOnly || n.PolicyRevision != 2 {
		t.Fatalf("isolation: %v %+v", err, n)
	}

	p1 := Peer{ID: "p1", TokenHash: "h1", Network: "n", Roles: []proto.Role{proto.RoleExit}, Tags: []string{"tag:exit"},
		Labels: map[string]string{"k": "v"}, CreatedAt: now}
	if err := s.CreatePeer(ctx, p1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePeer(ctx, Peer{ID: "p2", TokenHash: "h2", Network: "n", Roles: []proto.Role{proto.RoleNode}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePeer(ctx, "p1", func(p *Peer) error { p.Address = "100.64.0.1/32"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePeer(ctx, "p2", func(p *Peer) error { p.Address = "100.64.0.1/32"; return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate address: %v", err)
	}
	got, err := s.GetPeerByTokenHash(ctx, "h1")
	if err != nil || got.Labels["k"] != "v" || got.Address != "100.64.0.1/32" || !got.CreatedAt.Equal(now) || got.Tags[0] != "tag:exit" {
		t.Fatalf("peer: %v %+v", err, got)
	}
	h := proto.Health{Links: []proto.LinkHealth{{PeerID: "hub", State: proto.LinkConnected, CandidateType: "host", RTTSeconds: 0.01}},
		Exit: &proto.ExitHealth{CapUsedBytes: 42, Paused: true}}
	if err := s.SetPeerHealth(ctx, "p1", h, now); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetPeer(ctx, "p1"); got.Health == nil || got.Health.Exit.CapUsedBytes != 42 || got.HealthAt == nil || got.LastSeen == nil {
		t.Fatalf("health: %+v", got)
	}
	if _, err := s.UpdatePeer(ctx, "p1", func(p *Peer) error { p.RevokedAt = &now; return nil }); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.ListPeers(ctx, PeerFilter{Network: "n"}); len(ps) != 1 {
		t.Fatalf("live peers: %d", len(ps))
	}
	if used, _ := s.UsedAddresses(ctx, "n"); len(used) != 1 {
		t.Fatalf("used addresses: %v", used)
	}

	k := AuthKey{ID: "k1", KeyHash: "kh", Network: "n", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateAuthKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseAuthKey(ctx, "k1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseAuthKey(ctx, "k1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second use of a single-use key: %v", err)
	}

	e := Enrollment{CodeHash: "c", PollHash: "p", Network: "n", Status: EnrollmentPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateEnrollment(ctx, e); err != nil {
		t.Fatal(err)
	}
	if e, err = s.UpdateEnrollment(ctx, "c", func(e *Enrollment) error { e.Status = EnrollmentApproved; return nil }); err != nil || e.Status != EnrollmentApproved {
		t.Fatalf("enrolment: %v %+v", err, e)
	}
	if e, err = s.GetEnrollmentByPoll(ctx, "p"); err != nil || e.Status != EnrollmentApproved {
		t.Fatalf("enrolment by poll: %v %+v", err, e)
	}

	if err := s.AppendAudit(ctx, AuditEvent{ID: "a1", Time: now, Network: "n", Actor: "system", Action: "x", Detail: map[string]any{"k": 1}}); err != nil {
		t.Fatal(err)
	}
	if evs, err := s.ListAudit(ctx, AuditFilter{Network: "n"}); err != nil || len(evs) != 1 || evs[0].Action != "x" {
		t.Fatalf("audit: %v %+v", err, evs)
	}
	st, err := s.Stats(ctx)
	if err != nil || st.Peers != 2 || st.RevokedPeers != 1 || st.Networks != 1 {
		t.Fatalf("stats: %v %+v", err, st)
	}
	if err := s.DeleteNetwork(ctx, "n"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleting a network with peers: %v", err)
	}
	_ = s.Close()

	// Reopening does not apply a migration twice.
	s2, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()
}
