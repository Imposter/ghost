package control

import (
	"context"
	"errors"
	"time"

	"github.com/Imposter/ghost/ghost-go/signal/proto"

	"github.com/Imposter/ghost/ghost-server/server/store"
)

// Sweep runs periodic housekeeping once:
//
//   - peers whose credentials expired while online are disconnected;
//   - ephemeral peers offline for longer than the grace period are deleted;
//   - enrolments that expired a day ago are removed.
func (s *Service) Sweep(ctx context.Context) {
	ctx = WithActor(ctx, "system")
	now := s.now()
	peers, err := s.st.ListPeers(ctx, store.PeerFilter{})
	if err != nil {
		s.log.Warn("janitor: list peers", "error", err)
		return
	}
	live := s.live()
	for _, p := range peers {
		online := live != nil && live.Online(p.ID)
		switch {
		case p.Status(now) == store.PeerExpired && online:
			s.expired(ctx, p)
		case p.Ephemeral && !online && s.ephemeralStale(p, now):
			if err := s.DeletePeer(ctx, p.ID); err != nil {
				s.log.Warn("janitor: delete ephemeral peer", "peer", p.ID, "error", err)
			}
		}
	}
	if _, err := s.st.DeleteEnrollmentsBefore(ctx, now.Add(-24*time.Hour)); err != nil {
		s.log.Warn("janitor: sweep enrolments", "error", err)
	}
}

// ephemeralStale reports whether an offline ephemeral peer is past its grace
// period (measured from its last sighting, or its creation if never seen).
func (s *Service) ephemeralStale(p store.Peer, now time.Time) bool {
	since := p.CreatedAt
	if p.LastSeen != nil {
		since = *p.LastSeen
	}
	return now.Sub(since) >= s.ephemeralGrace
}

// DisconnectCodeFor maps an authentication error to the fatal signalling
// error code the relay sends.
func DisconnectCodeFor(err error) string {
	switch {
	case errors.Is(err, ErrPeerRevoked):
		return proto.ErrCodeRevoked
	case errors.Is(err, ErrPeerExpired):
		return proto.ErrCodeExpired
	}
	return proto.ErrCodeUnauthorized
}
