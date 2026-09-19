package ghost

// Node is a member that joins a network and connects to the peers its netmap
// lists; with the default ACLs, that is the network's hubs. Its roles (node,
// exit, relay) come from the control plane. A Node exposes DialContext and
// Listen on its tunnel netstack.
type Node struct {
	*mesh
}

// NewNode creates a Node from cfg. Call Start to connect.
func NewNode(cfg Config) (*Node, error) {
	m, err := newMesh(cfg, nil)
	if err != nil {
		return nil, err
	}
	return &Node{mesh: m}, nil
}
