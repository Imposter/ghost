package ghost

import "github.com/Imposter/ghost/ghost-go/signal/proto"

// Hub is a member that holds the hub role: a gateway many peers connect to.
// Like every member it connects to the peers its netmap lists (for a hub,
// normally every peer the ACLs let it reach) and runs one ICE agent, one
// MultiBind entry and one WireGuard peer per link on a single netstack. A Hub
// exposes DialContext and Listen on its tunnel netstack, so a process on the
// hub can reach any linked peer's tunnel address (for example to scrape a
// node's in-tunnel metrics endpoint).
type Hub struct {
	*mesh
}

// NewHub creates a Hub from cfg. Call Start to connect. The control plane must
// have assigned the peer the hub role; otherwise its hello is rejected.
func NewHub(cfg Config) (*Hub, error) {
	m, err := newMesh(cfg, []proto.Role{proto.RoleHub})
	if err != nil {
		return nil, err
	}
	return &Hub{mesh: m}, nil
}

// Peers returns the peer ids of currently connected tunnel peers.
func (h *Hub) Peers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.links))
	for id, l := range h.links {
		if l.added {
			out = append(out, id)
		}
	}
	return out
}
