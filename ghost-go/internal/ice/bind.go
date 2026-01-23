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

// Network buffer size constants.
const (
	// ReceiveChannelBufferSize is the channel buffer for received packets.
	// 32 provides sufficient buffering for typical WireGuard traffic patterns
	// without excessive memory usage.
	ReceiveChannelBufferSize = 32

	// MaxUDPPacketSize is the maximum size of a UDP packet.
	// This is the theoretical maximum for IPv4/IPv6 UDP (64KB - headers).
	MaxUDPPacketSize = 65535
)

// ICEBind implements WireGuard's conn.Bind interface using an ICE connection.
// This is the critical adapter that bridges ICE and WireGuard.
//
// Design decisions:
//   - Single endpoint per bind (ICE connections are point-to-point)
//   - BatchSize of 1 (simplifies implementation, sufficient for initial version)
//   - Direct reads from net.Conn (no buffering)
//   - No SetMark support (not applicable for ICE)
//   - Supports close/reopen cycle (wireguard-go rebinds on config changes)
//   - Does NOT own the underlying connection (caller is responsible for closing it)
//
// Ownership: ICEBind does NOT close the underlying net.Conn. The caller who
// created the connection is responsible for closing it. This follows Go's
// convention that whoever creates a resource is responsible for cleaning it up.
type ICEBind struct {
	conn     net.Conn
	endpoint *ICEEndpoint
	logger   *slog.Logger

	mu       sync.RWMutex
	open     bool // Whether the bind is currently open (receive loop running)
	recvChan chan []byte
	stopChan chan struct{}
}

// NewICEBind creates a new ICEBind from an established ICE connection.
// Returns nil if the connection is nil or invalid (closed).
func NewICEBind(conn net.Conn, logger *slog.Logger) *ICEBind {
	if conn == nil {
		return nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Check if connection is still valid - closed connections return nil addresses
	localAddr := conn.LocalAddr()
	remoteAddr := conn.RemoteAddr()
	if localAddr == nil || remoteAddr == nil {
		logger.Error("NewICEBind called with invalid/closed connection",
			"localAddr", localAddr,
			"remoteAddr", remoteAddr)
		return nil
	}

	endpoint := NewICEEndpoint(remoteAddr)

	logger.Info("ICEBind created",
		"local", localAddr.String(),
		"remote", remoteAddr.String(),
	)

	return &ICEBind{
		conn:     conn,
		endpoint: endpoint,
		logger:   logger,
		recvChan: make(chan []byte, ReceiveChannelBufferSize),
		stopChan: make(chan struct{}),
	}
}

// Open initializes the bind and starts receiving packets.
// The port parameter is ignored since ICE manages the socket.
// This method can be called multiple times (wireguard-go rebinds on config changes).
func (b *ICEBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.open {
		// Already open, just return the receive function
		b.logger.Debug("ICEBind already open, returning existing receive func")
		receiveFuncs := []conn.ReceiveFunc{b.makeReceiveFunc()}
		localPort := b.getLocalPort()
		return receiveFuncs, localPort, nil
	}

	b.logger.Info("Opening ICEBind", "requested_port", port)

	// Create fresh channels for this open cycle
	b.recvChan = make(chan []byte, ReceiveChannelBufferSize)
	b.stopChan = make(chan struct{})
	b.open = true

	// Start receive goroutine
	go b.receiveLoop()

	// Return a single receive function
	receiveFuncs := []conn.ReceiveFunc{b.makeReceiveFunc()}

	localPort := b.getLocalPort()
	b.logger.Info("ICEBind opened", "port", localPort)

	return receiveFuncs, localPort, nil
}

// getLocalPort returns the local port from the connection.
// Must be called with the lock held.
func (b *ICEBind) getLocalPort() uint16 {
	localAddr := b.conn.LocalAddr()
	switch addr := localAddr.(type) {
	case *net.UDPAddr:
		return uint16(addr.Port)
	case *net.TCPAddr:
		return uint16(addr.Port)
	default:
		return 0
	}
}

// makeReceiveFunc creates a receive function for WireGuard.
func (b *ICEBind) makeReceiveFunc() conn.ReceiveFunc {
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		b.mu.RLock()
		if !b.open {
			b.mu.RUnlock()
			return 0, net.ErrClosed
		}
		recvChan := b.recvChan
		stopChan := b.stopChan
		b.mu.RUnlock()

		// Wait for a packet
		select {
		case packet, ok := <-recvChan:
			if !ok {
				return 0, net.ErrClosed
			}
			if len(bufs) == 0 {
				return 0, nil
			}

			// Copy packet to buffer
			n := copy(bufs[0], packet)
			sizes[0] = n
			eps[0] = b.endpoint

			b.logger.Debug("Received packet", "size", n)
			return 1, nil

		case <-stopChan:
			return 0, net.ErrClosed
		}
	}
}

// receiveLoop continuously reads packets from the ICE connection.
func (b *ICEBind) receiveLoop() {
	defer b.logger.Debug("Receive loop stopped")

	buffer := make([]byte, MaxUDPPacketSize)

	for {
		// Check if we should stop before reading
		b.mu.RLock()
		if !b.open {
			b.mu.RUnlock()
			return
		}
		stopChan := b.stopChan
		recvChan := b.recvChan
		b.mu.RUnlock()

		n, err := b.conn.Read(buffer)
		if err != nil {
			b.mu.RLock()
			open := b.open
			b.mu.RUnlock()

			if !open {
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
		case recvChan <- packet:
		case <-stopChan:
			return
		default:
			// Channel full, drop packet
			b.logger.Warn("Receive channel full, dropping packet", "size", n)
		}
	}
}

// Send sends packets to the remote endpoint.
func (b *ICEBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
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

// Close stops the receive loop. Does NOT close the underlying connection.
//
// The caller who created the net.Conn is responsible for closing it.
// This follows Go's convention that whoever creates a resource is responsible
// for cleaning it up.
//
// This method can be called multiple times safely (idempotent).
// After Close(), the bind can be reopened by calling Open() again.
func (b *ICEBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.open {
		// Already closed
		return nil
	}

	b.open = false
	close(b.stopChan)

	b.logger.Debug("ICEBind closed (receive loop stopped)")

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
