package ghost

import (
	"context"
	"fmt"
	"net"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Node is a member that joins a network and connects to a single hub. Traffic
// to the whole network pool is routed through that hub. A Node exposes
// DialContext and Listen on its tunnel netstack.
type Node struct {
	*mesh
}

// NewNode creates a Node from cfg. Call Start to connect.
func NewNode(cfg Config) (*Node, error) {
	m, err := newMesh(cfg, proto.RoleNode)
	if err != nil {
		return nil, err
	}
	return &Node{mesh: m}, nil
}

// DialContext dials addr ("host:port") over the tunnel netstack. Connections
// are carried through the hub to their destination.
func (n *Node) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ns := n.Netstack()
	if ns == nil {
		return nil, fmt.Errorf("node not joined")
	}
	return netstackDialContext(ctx, ns, network, addr)
}

// Listen listens on addr over the tunnel netstack.
func (n *Node) Listen(network, addr string) (net.Listener, error) {
	ns := n.Netstack()
	if ns == nil {
		return nil, fmt.Errorf("node not joined")
	}
	tcpAddr, err := resolveTCPAddr(addr)
	if err != nil {
		return nil, err
	}
	return ns.ListenTCP(tcpAddr)
}
