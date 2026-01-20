package ice

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
)

// ICEBind implements WireGuard's conn.Bind interface using an ICE connection.
// This is the critical adapter that bridges ICE and WireGuard.
//
// Design decisions:
//   - Single endpoint per bind (ICE connections are point-to-point)
//   - BatchSize of 1 (simplifies implementation, sufficient for initial version)
//   - Direct reads from net.Conn (no buffering)
//   - No SetMark support (not applicable for ICE)
type ICEBind struct {
	conn     net.Conn
	endpoint *ICEEndpoint
	logger   *slog.Logger

	mu       sync.RWMutex
	closed   bool
	recvChan chan []byte
	stopChan chan struct{}
}

// NewICEBind creates a new ICEBind from an established ICE connection.
func NewICEBind(conn net.Conn, logger *slog.Logger) *ICEBind {
	if logger == nil {
		logger = slog.Default()
	}

	endpoint := NewICEEndpoint(conn.RemoteAddr())

	logger.Info("ICEBind created",
		"local", conn.LocalAddr().String(),
		"remote", conn.RemoteAddr().String(),
	)

	return &ICEBind{
		conn:     conn,
		endpoint: endpoint,
		logger:   logger,
		recvChan: make(chan []byte, 32), // Buffer for received packets
		stopChan: make(chan struct{}),
	}
}

// Open initializes the bind and starts receiving packets.
// The port parameter is ignored since ICE manages the socket.
func (b *ICEBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, 0, errors.New("bind is closed")
	}

	b.logger.Info("Opening ICEBind", "requested_port", port)

	// Start receive goroutine
	go b.receiveLoop()

	// Return a single receive function
	receiveFuncs := []conn.ReceiveFunc{b.makeReceiveFunc()}

	// Port is fixed by ICE connection
	localAddr := b.conn.LocalAddr()
	var localPort uint16
	switch addr := localAddr.(type) {
	case *net.UDPAddr:
		localPort = uint16(addr.Port)
	case *net.TCPAddr:
		localPort = uint16(addr.Port)
	default:
		localPort = 0
	}

	b.logger.Info("ICEBind opened", "port", localPort)

	return receiveFuncs, localPort, nil
}

// makeReceiveFunc creates a receive function for WireGuard.
func (b *ICEBind) makeReceiveFunc() conn.ReceiveFunc {
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		b.mu.RLock()
		if b.closed {
			b.mu.RUnlock()
			return 0, net.ErrClosed
		}
		b.mu.RUnlock()

		// Wait for a packet
		select {
		case packet := <-b.recvChan:
			if len(bufs) == 0 {
				return 0, nil
			}

			// Copy packet to buffer
			n := copy(bufs[0], packet)
			sizes[0] = n
			eps[0] = b.endpoint

			b.logger.Debug("Received packet", "size", n)
			return 1, nil

		case <-b.stopChan:
			return 0, net.ErrClosed
		}
	}
}

// receiveLoop continuously reads packets from the ICE connection.
func (b *ICEBind) receiveLoop() {
	defer b.logger.Info("Receive loop stopped")

	buffer := make([]byte, 65535) // Max UDP packet size

	for {
		n, err := b.conn.Read(buffer)
		if err != nil {
			b.mu.RLock()
			closed := b.closed
			b.mu.RUnlock()

			if closed {
				return
			}

			if errors.Is(err, net.ErrClosed) {
				return
			}

			b.logger.Warn("Read error", "error", err)
			continue
		}

		// Copy packet (buffer is reused)
		packet := make([]byte, n)
		copy(packet, buffer[:n])

		// Try to send to channel (non-blocking)
		select {
		case b.recvChan <- packet:
		case <-b.stopChan:
			return
		default:
			// Channel full, drop packet
			b.logger.Warn("Receive channel full, dropping packet", "size", n)
		}
	}
}

// Send sends packets to the remote endpoint.
func (b *ICEBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return net.ErrClosed
	}

	// ICE connection is point-to-point, endpoint should match
	// Type assertion needed since endpoint is an interface
	if endpoint != nil {
		if iceEp, ok := endpoint.(*ICEEndpoint); ok && iceEp != b.endpoint {
			return fmt.Errorf("endpoint mismatch: expected %v, got %v", b.endpoint, endpoint)
		}
	}

	// Send all buffers
	var totalSent int
	for _, buf := range bufs {
		n, err := b.conn.Write(buf)
		if err != nil {
			return fmt.Errorf("failed to send packet: %w", err)
		}
		totalSent += n
	}

	b.logger.Debug("Sent packets", "count", len(bufs), "total_bytes", totalSent)
	return nil
}

// Close closes the bind and releases all resources.
func (b *ICEBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true
	close(b.stopChan)

	b.logger.Info("Closing ICEBind")

	// Close the underlying connection
	if err := b.conn.Close(); err != nil {
		return fmt.Errorf("failed to close connection: %w", err)
	}

	return nil
}

// SetMark sets the mark on the socket (no-op for ICE).
// WireGuard uses this for policy routing, but ICE handles routing internally.
func (b *ICEBind) SetMark(mark uint32) error {
	// Not applicable for ICE connections
	return nil
}

// BatchSize returns the maximum number of packets that can be sent/received at once.
// We use 1 for simplicity in the initial implementation.
func (b *ICEBind) BatchSize() int {
	return 1
}

// ParseEndpoint parses an endpoint string.
// For ICE, we use a fixed endpoint, so this returns our pre-configured endpoint.
func (b *ICEBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	// For ICE, endpoint is fixed
	if s == "" || s == b.endpoint.DstToString() {
		return b.endpoint, nil
	}

	// Try to parse as IP:port for compatibility
	addr, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint format: %w", err)
	}

	// Create UDP address
	udpAddr := net.UDPAddrFromAddrPort(addr)
	return NewICEEndpoint(udpAddr), nil
}
