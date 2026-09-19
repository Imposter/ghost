package ghost

import (
	"context"
	"fmt"
	"net"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Hub is a member that accepts many nodes. It runs one ICE agent and one
// MultiBind entry per node, and a single WireGuard device on a netstack with a
// WireGuard peer per node. A Hub exposes DialContext and Listen on its tunnel
// netstack, so a process on the hub can reach any node's tunnel address (for
// example to scrape a node's in-tunnel metrics endpoint).
type Hub struct {
	*mesh
}

// NewHub creates a Hub from cfg. Call Start to connect.
func NewHub(cfg Config) (*Hub, error) {
	m, err := newMesh(cfg, proto.RoleHub)
	if err != nil {
		return nil, err
	}
	return &Hub{mesh: m}, nil
}

// DialContext dials addr ("host:port") over the tunnel netstack.
func (h *Hub) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ns := h.Netstack()
	if ns == nil {
		return nil, fmt.Errorf("hub not joined")
	}
	return netstackDialContext(ctx, ns, network, addr)
}

// Listen listens on addr over the tunnel netstack.
func (h *Hub) Listen(network, addr string) (net.Listener, error) {
	ns := h.Netstack()
	if ns == nil {
		return nil, fmt.Errorf("hub not joined")
	}
	tcpAddr, err := resolveTCPAddr(addr)
	if err != nil {
		return nil, err
	}
	return ns.ListenTCP(tcpAddr)
}

// Peers returns the device ids of currently connected nodes.
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
