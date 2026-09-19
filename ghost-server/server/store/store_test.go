package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func TestSQLiteStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateNetwork(ctx, Network{Name: "n", Pool: "100.64.0.0/10", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNetwork(ctx, Network{Name: "n", Pool: "x", CreatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate network: %v", err)
	}
	p, err := s.SetNetworkPolicy(ctx, "n", proto.ExitPolicy{Allow: []string{"a:443"}})
	if err != nil || p.Revision != 1 {
		t.Fatalf("policy: %v %+v", err, p)
	}

	d := Device{ID: "d1", TokenHash: "h1", Network: "n", Role: proto.RoleNode, Labels: map[string]string{"k": "v"}, CreatedAt: now}
	if err := s.CreateDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDevice(ctx, Device{ID: "d2", TokenHash: "h2", Network: "n", Role: proto.RoleNode, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceAddress(ctx, "d1", "100.64.0.1/32"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceAddress(ctx, "d2", "100.64.0.1/32"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate address: %v", err)
	}
	got, err := s.GetDeviceByTokenHash(ctx, "h1")
	if err != nil || got.Labels["k"] != "v" || got.Address != "100.64.0.1/32" || !got.CreatedAt.Equal(now) {
		t.Fatalf("device: %v %+v", err, got)
	}
	if err := s.RevokeDevice(ctx, "d1", now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(ctx, "d1", now.Add(time.Hour)); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if got, _ := s.GetDevice(ctx, "d1"); got.RevokedAt == nil || !got.RevokedAt.Equal(now) {
		t.Fatalf("revoked_at: %+v", got.RevokedAt)
	}
	if ds, _ := s.ListDevices(ctx, DeviceFilter{Network: "n"}); len(ds) != 1 {
		t.Fatalf("live devices: %d", len(ds))
	}

	pc := PairingCode{CodeHash: "c", Network: "n", Role: proto.RoleNode, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreatePairingCode(ctx, pc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairingCode(ctx, "c", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPairingCode(ctx, "c", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second redeem: %v", err)
	}
	st, err := s.Stats(ctx)
	if err != nil || st.Devices != 2 || st.RevokedDevices != 1 || st.Networks != 1 {
		t.Fatalf("stats: %v %+v", err, st)
	}
	_ = s.Close()

	// Reopening does not apply a migration twice.
	s2, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()
}
