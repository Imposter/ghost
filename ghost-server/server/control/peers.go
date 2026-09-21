package control

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/ipam"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// PeerSummary is the peer shape carried by bus events.
type PeerSummary struct {
	ID      string           `json:"id"`
	Network string           `json:"network"`
	Name    string           `json:"name,omitempty"`
	Roles   []proto.Role     `json:"roles"`
	Tags    []string         `json:"tags,omitempty"`
	Address string           `json:"address,omitempty"`
	Status  store.PeerStatus `json:"status"`
}

// Summary returns the event form of p.
func (s *Service) Summary(p store.Peer) PeerSummary {
	return PeerSummary{ID: p.ID, Network: p.Network, Name: p.Name, Roles: p.Roles, Tags: p.Tags,
		Address: p.Address, Status: p.Status(s.now())}
}

func (s *Service) publishPeer(eventType string, p store.Peer, extra map[string]any) {
	data := map[string]any{"peer": s.Summary(p)}
	for k, v := range extra {
		data[k] = v
	}
	s.bus.Publish(events.Event{Type: eventType, Network: p.Network, PeerID: p.ID, Data: data})
}

// ValidWireGuardKey reports whether k is a base64 32-byte key.
func ValidWireGuardKey(k string) bool {
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

// Authenticate resolves a peer token to an active peer.
func (s *Service) Authenticate(ctx context.Context, token string) (store.Peer, error) {
	if token == "" {
		return store.Peer{}, ErrUnauthorized
	}
	p, err := s.st.GetPeerByTokenHash(ctx, HashSecret(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return p, ErrUnauthorized
		}
		return p, err
	}
	switch p.Status(s.now()) {
	case store.PeerRevoked:
		return p, ErrPeerRevoked
	case store.PeerExpired:
		return p, ErrPeerExpired
	}
	return p, nil
}

// Peer returns one peer.
func (s *Service) Peer(ctx context.Context, id string) (store.Peer, error) {
	p, err := s.st.GetPeer(ctx, id)
	return p, mapStoreErr(err)
}

// PeerQuery narrows ListPeers.
type PeerQuery struct {
	Network        string
	Tag            string
	Role           proto.Role
	IncludeRevoked bool
}

// ListPeers returns peers matching q.
func (s *Service) ListPeers(ctx context.Context, q PeerQuery) ([]store.Peer, error) {
	all, err := s.st.ListPeers(ctx, store.PeerFilter{Network: q.Network, IncludeRevoked: q.IncludeRevoked})
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, p := range all {
		if q.Tag != "" && !slices.Contains(p.Tags, q.Tag) {
			continue
		}
		if q.Role != "" && !proto.HasRole(p.Roles, q.Role) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// PeerPatch changes a peer. Nil fields are unchanged; ClearExpiry removes an
// expiry.
type PeerPatch struct {
	Name        *string            `json:"name"`
	Roles       *[]proto.Role      `json:"roles"`
	Tags        *[]string          `json:"tags"`
	Labels      *map[string]string `json:"labels"`
	ExpiresAt   *time.Time         `json:"expires_at"`
	ClearExpiry bool               `json:"clear_expiry"`
}

// UpdatePeer applies a patch, then pushes netmaps. A peer whose new expiry has
// already passed is disconnected.
func (s *Service) UpdatePeer(ctx context.Context, id string, patch PeerPatch) (store.Peer, error) {
	current, err := s.Peer(ctx, id)
	if err != nil {
		return current, err
	}
	var roles []proto.Role
	if patch.Roles != nil {
		if roles, err = normalizeRoles(*patch.Roles); err != nil {
			return current, err
		}
	}
	if patch.Tags != nil {
		if err := s.checkTags(ctx, current.Network, *patch.Tags); err != nil {
			return current, err
		}
	}
	if patch.Labels != nil {
		if err := validLabels(*patch.Labels); err != nil {
			return current, err
		}
	}
	p, err := s.st.UpdatePeer(ctx, id, func(p *store.Peer) error {
		if patch.Name != nil {
			p.Name = *patch.Name
		}
		if patch.Roles != nil {
			p.Roles = roles
		}
		if patch.Tags != nil {
			p.Tags = unionTags(*patch.Tags)
		}
		if patch.Labels != nil {
			p.Labels = mergeLabels(*patch.Labels)
		}
		switch {
		case patch.ClearExpiry:
			p.ExpiresAt = nil
		case patch.ExpiresAt != nil:
			t := patch.ExpiresAt.UTC()
			p.ExpiresAt = &t
		}
		return nil
	})
	if err != nil {
		return p, mapStoreErr(err)
	}
	s.access.Forget(id)
	s.Audit(ctx, p.Network, "peer.updated", id, map[string]any{"roles": p.Roles, "tags": p.Tags, "expires_at": p.ExpiresAt})
	s.publishPeer(events.PeerUpdated, p, nil)
	if p.Status(s.now()) == store.PeerExpired {
		s.disconnect(id, proto.ErrCodeExpired, "peer credentials expired")
	}
	s.networkChanged(ctx, p.Network)
	return p, nil
}

// MovePeer moves a peer to another network: its address is released (a new
// one is assigned on its next join) and its live session is closed.
func (s *Service) MovePeer(ctx context.Context, id, network string) (store.Peer, error) {
	current, err := s.Peer(ctx, id)
	if err != nil {
		return current, err
	}
	if err := s.checkTags(ctx, network, current.Tags); err != nil {
		return current, err
	}
	s.allocMu.Lock()
	p, err := s.st.UpdatePeer(ctx, id, func(p *store.Peer) error {
		p.Network, p.Address = network, ""
		return nil
	})
	s.allocMu.Unlock()
	if err != nil {
		return p, mapStoreErr(err)
	}
	s.access.Forget(id)
	s.disconnect(id, proto.ErrCodeMoved, "peer moved to network "+network)
	s.Audit(ctx, network, "peer.moved", id, map[string]any{"from": current.Network, "to": network})
	s.publishPeer(events.PeerUpdated, p, map[string]any{"moved_from": current.Network})
	s.networkChanged(ctx, current.Network)
	s.networkChanged(ctx, network)
	return p, nil
}

// RevokePeer revokes a peer and disconnects its live session immediately.
func (s *Service) RevokePeer(ctx context.Context, id string) (store.Peer, error) {
	p, err := s.st.UpdatePeer(ctx, id, func(p *store.Peer) error {
		if p.RevokedAt == nil {
			t := s.now().UTC()
			p.RevokedAt = &t
		}
		return nil
	})
	if err != nil {
		return p, mapStoreErr(err)
	}
	s.access.Forget(id)
	// Announced before the disconnect, whose peer.offline follows: a watcher
	// sees why a peer went away before it sees that it did.
	s.metrics.Revoked(ctx)
	s.Audit(ctx, p.Network, "peer.revoked", id, nil)
	s.publishPeer(events.PeerRevoked, p, nil)
	s.disconnect(id, proto.ErrCodeRevoked, "peer revoked")
	s.networkChanged(ctx, p.Network)
	return p, nil
}

// ExpirePeer expires a peer's credentials now and disconnects it. Unlike a
// revocation, an expiry can be lifted with UpdatePeer.
func (s *Service) ExpirePeer(ctx context.Context, id string) (store.Peer, error) {
	now := s.now().UTC()
	p, err := s.st.UpdatePeer(ctx, id, func(p *store.Peer) error {
		p.ExpiresAt = &now
		return nil
	})
	if err != nil {
		return p, mapStoreErr(err)
	}
	s.expired(ctx, p)
	return p, nil
}

func (s *Service) expired(ctx context.Context, p store.Peer) {
	s.access.Forget(p.ID)
	// Announced before the disconnect, as a revocation is.
	s.Audit(ctx, p.Network, "peer.expired", p.ID, map[string]any{"expires_at": p.ExpiresAt})
	s.publishPeer(events.PeerExpired, p, nil)
	s.disconnect(p.ID, proto.ErrCodeExpired, "peer credentials expired")
	s.networkChanged(ctx, p.Network)
}

// DeletePeer deletes a peer and disconnects its live session.
func (s *Service) DeletePeer(ctx context.Context, id string) error {
	p, err := s.Peer(ctx, id)
	if err != nil {
		return err
	}
	if err := s.st.DeletePeer(ctx, id); err != nil {
		return mapStoreErr(err)
	}
	s.access.Forget(id)
	s.disconnect(id, proto.ErrCodeRevoked, "peer deleted")
	s.Audit(ctx, p.Network, "peer.deleted", id, nil)
	s.publishPeer(events.PeerDeleted, p, nil)
	s.networkChanged(ctx, p.Network)
	return nil
}

// Rotation is the result of a credential rotation.
type Rotation struct {
	PeerID    string `json:"peer_id"`
	PeerToken string `json:"peer_token"`
	PublicKey string `json:"public_key,omitempty"`
}

// RotateCredentials replaces a peer's token (the old one stops working at
// once) and, when publicKey is set, its WireGuard key. Peers that see it get
// the new key in a netmap delta.
func (s *Service) RotateCredentials(ctx context.Context, peer store.Peer, publicKey string) (Rotation, error) {
	if publicKey != "" && !ValidWireGuardKey(publicKey) {
		return Rotation{}, invalidf("public_key must be a base64 32-byte key")
	}
	token := newSecret(PeerTokenPrefix)
	p, err := s.st.UpdatePeer(ctx, peer.ID, func(p *store.Peer) error {
		p.TokenHash = HashSecret(token)
		if publicKey != "" {
			p.PublicKey = publicKey
		}
		return nil
	})
	if err != nil {
		return Rotation{}, mapStoreErr(err)
	}
	s.Audit(ctx, p.Network, "peer.credentials_rotated", p.ID, map[string]any{"key_rotated": publicKey != ""})
	s.publishPeer(events.PeerKeyRotated, p, map[string]any{"key_rotated": publicKey != ""})
	if publicKey != "" && publicKey != peer.PublicKey {
		s.networkChanged(ctx, p.Network)
	}
	return Rotation{PeerID: p.ID, PeerToken: token, PublicKey: p.PublicKey}, nil
}

// SetPublicKey records the WireGuard key a peer presented in its hello. A
// changed key is a key rotation: it is audited and pushed to the peers that
// see this one once the session joins.
func (s *Service) SetPublicKey(ctx context.Context, peer store.Peer, key string) (store.Peer, error) {
	if !ValidWireGuardKey(key) {
		return peer, invalidf("public_key must be a base64 32-byte key")
	}
	if key == peer.PublicKey {
		return peer, nil
	}
	p, err := s.st.UpdatePeer(ctx, peer.ID, func(p *store.Peer) error {
		p.PublicKey = key
		return nil
	})
	if err != nil {
		return p, mapStoreErr(err)
	}
	if peer.PublicKey != "" {
		s.Audit(ctx, p.Network, "peer.key_rotated", p.ID, nil)
		s.publishPeer(events.PeerKeyRotated, p, map[string]any{"key_rotated": true})
		s.networkChanged(ctx, p.Network)
	}
	return p, nil
}

// EnsureAddress returns the peer's address in its network, assigning one from
// the pool on first use.
func (s *Service) EnsureAddress(ctx context.Context, peerID string) (store.Peer, error) {
	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	current, err := s.st.GetPeer(ctx, peerID)
	if err != nil {
		return current, mapStoreErr(err)
	}
	n, err := s.st.GetNetwork(ctx, current.Network)
	if err != nil {
		return current, mapStoreErr(err)
	}
	pool, err := ipam.ParsePool(n.Pool)
	if err != nil {
		return current, err
	}
	if current.Address != "" && ipam.Contains(pool, current.Address) {
		return current, nil
	}
	used, err := s.st.UsedAddresses(ctx, current.Network)
	if err != nil {
		return current, err
	}
	addr, err := ipam.Allocate(pool, used)
	if err != nil {
		return current, err
	}
	p, err := s.st.UpdatePeer(ctx, peerID, func(p *store.Peer) error {
		p.Address = addr
		return nil
	})
	return p, mapStoreErr(err)
}

// RecordHealth stores a peer's heartbeat health summary and publishes it.
// Endpoint changes are persisted with it.
func (s *Service) RecordHealth(ctx context.Context, p store.Peer, h proto.Health) {
	now := s.now().UTC()
	if err := s.st.SetPeerHealth(ctx, p.ID, h, now); err != nil {
		s.log.Warn("health: store", "peer", p.ID, "error", err)
		return
	}
	if len(h.Endpoints) > 0 && !slices.Equal(h.Endpoints, p.Endpoints) {
		_, _ = s.st.UpdatePeer(ctx, p.ID, func(sp *store.Peer) error {
			sp.Endpoints = slices.Clone(h.Endpoints)
			return nil
		})
	}
	s.bus.Publish(events.Event{Type: events.PeerHealth, Network: p.Network, PeerID: p.ID, Time: now, Data: h})
}
