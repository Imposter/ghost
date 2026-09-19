// Package ghost is the public API of the ghost library: a Node or Hub joins a
// network through a Signaller (by default the ghost-server control plane; see
// package ghost/direct for peer to peer with no server), links to the peers
// its netmap lists over ICE, and runs WireGuard end to end on a userspace
// netstack. DialContext and Listen work on that netstack; no OS TUN device or
// privileges are needed.
package ghost

// Node is a member that joins a network and connects to the peers its netmap
// lists; with the default ACLs, that is the network's hubs. Its roles (node,
// exit, relay) come from the control plane; Config.Roles only asks for them.
// A Node exposes DialContext and Listen on its tunnel netstack.
type Node struct {
	*mesh
}

// NewNode creates a Node from cfg. Call Start to connect.
func NewNode(cfg Config) (*Node, error) {
	m, err := newMesh(cfg)
	if err != nil {
		return nil, err
	}
	return &Node{mesh: m}, nil
}
