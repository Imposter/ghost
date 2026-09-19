package control

import (
	"context"
	"sort"

	"github.com/Imposter/ghost/ghost-server/server/access"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

// Reauthorization is the outcome of re-asking the authorizer about one peer.
type Reauthorization struct {
	PeerID  string `json:"peer_id"`
	Network string `json:"network"`
	// Online reports whether the peer had a joined session. An offline peer is
	// not asked about; the authorizer decides again when it next joins.
	Online bool `json:"online"`
	// Allowed is the authorizer's answer for an online peer. A denial (an
	// unreachable authorizer included) closes the session with a fatal
	// forbidden error, which Disconnected records.
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason,omitempty"`
	Disconnected bool   `json:"disconnected"`
}

func reauthorization(peerID, network string, d access.Decision, online bool) Reauthorization {
	r := Reauthorization{PeerID: peerID, Network: network, Online: online}
	if online {
		r.Allowed, r.Reason, r.Disconnected = d.Allow, d.Reason, !d.Allow
	}
	return r
}

// ReauthorizePeer re-asks the authorizer, uncached, whether the peer's live
// session may stay connected, and disconnects it if not. The authorizer is
// asked again on every join, so a peer it now refuses stays out until it
// allows it again. The peer's cached decisions are dropped first, so an
// offline peer is asked afresh on its next join. It returns ErrAuthorizerOpen
// in open mode.
func (s *Service) ReauthorizePeer(ctx context.Context, id string) (Reauthorization, error) {
	p, err := s.Peer(ctx, id)
	if err != nil {
		return Reauthorization{}, err
	}
	if !s.access.Consulted() {
		return Reauthorization{}, ErrAuthorizerOpen
	}
	s.access.Forget(p.ID)
	var (
		d      access.Decision
		online bool
	)
	if l := s.live(); l != nil {
		d, online = l.Reauthorize(ctx, p.ID)
	}
	r := reauthorization(p.ID, p.Network, d, online)
	s.Audit(ctx, p.Network, "peer.reauthorized", p.ID, map[string]any{
		"online": r.Online, "allowed": r.Allowed, "reason": r.Reason,
	})
	return r, nil
}

// ReauthorizeNetwork re-asks the authorizer about every live peer in a
// network, as ReauthorizePeer does for one, and returns the results ordered by
// peer id. Offline peers are not listed, but their cached decisions are
// dropped too. It returns ErrAuthorizerOpen in open mode.
func (s *Service) ReauthorizeNetwork(ctx context.Context, network string) ([]Reauthorization, error) {
	if _, err := s.Network(ctx, network); err != nil {
		return nil, err
	}
	if !s.access.Consulted() {
		return nil, ErrAuthorizerOpen
	}
	peers, err := s.st.ListPeers(ctx, store.PeerFilter{Network: network})
	if err != nil {
		return nil, err
	}
	for _, p := range peers {
		s.access.Forget(p.ID)
	}
	var decisions map[string]access.Decision
	if l := s.live(); l != nil {
		decisions = l.ReauthorizeNetwork(ctx, network)
	}
	out := make([]Reauthorization, 0, len(decisions))
	disconnected := 0
	for id, d := range decisions {
		r := reauthorization(id, network, d, true)
		if r.Disconnected {
			disconnected++
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	s.Audit(ctx, network, "network.reauthorized", network, map[string]any{
		"checked": len(out), "disconnected": disconnected,
	})
	return out, nil
}
