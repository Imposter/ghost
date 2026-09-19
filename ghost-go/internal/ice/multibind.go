package ice

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/conn"
)

// MultiBind implements WireGuard's conn.Bind over any number of point-to-point
// connections (typically one ICE connection per remote peer).
//
// Each connection is registered under an opaque endpoint key. The key is what
// WireGuard stores as the peer endpoint (via the UAPI "endpoint=" field), so
// packets sent to that peer go out on that connection, and packets read from
// that connection are reported as coming from that endpoint.
//
// Connections can be swapped at any time (for example after an ICE restart)
// without touching the WireGuard device. MultiBind never closes the
// connections it is given; their owner does.
type MultiBind struct {
	logger *slog.Logger

	mu    sync.RWMutex
	conns map[string]*bindConn
	open  bool
	stop  chan struct{}

	recv chan inbound
}

type inbound struct {
	data []byte
	ep   *MultiEndpoint
}

type bindConn struct {
	conn net.Conn
	ep   *MultiEndpoint
	done chan struct{}
}

// MultiEndpoint is the conn.Endpoint used by MultiBind. It is identified by
// its key; the address is informational.
type MultiEndpoint struct {
	key  string
	addr netip.AddrPort

	rx atomic.Uint64
	tx atomic.Uint64
}

// ClearSrc is a no-op.
func (e *MultiEndpoint) ClearSrc() {}

// SrcToString returns the endpoint key.
func (e *MultiEndpoint) SrcToString() string { return e.key }

// DstToString returns the endpoint key.
func (e *MultiEndpoint) DstToString() string { return e.key }

// DstToBytes returns the endpoint key as bytes.
func (e *MultiEndpoint) DstToBytes() []byte { return []byte(e.key) }

// DstIP returns the address encoded in the key, if any.
func (e *MultiEndpoint) DstIP() netip.Addr { return e.addr.Addr() }

// SrcIP returns the address encoded in the key, if any.
func (e *MultiEndpoint) SrcIP() netip.Addr { return e.addr.Addr() }

// NewMultiBind creates an empty MultiBind.
func NewMultiBind(logger *slog.Logger) *MultiBind {
	if logger == nil {
		logger = slog.Default()
	}
	return &MultiBind{
		logger: logger,
		conns:  make(map[string]*bindConn),
		recv:   make(chan inbound, 256),
		stop:   make(chan struct{}),
	}
}

// EndpointKey returns a stable, parseable endpoint key for index n. The key
// is a synthetic address in 240.0.0.0/4 (reserved, never routed).
func EndpointKey(n uint32) string {
	return fmt.Sprintf("240.%d.%d.%d:1", (n>>16)&0xff, (n>>8)&0xff, n&0xff)
}

// SetConn registers (or replaces) the connection used for key.
func (b *MultiBind) SetConn(key string, c net.Conn) {
	if c == nil {
		b.RemoveConn(key)
		return
	}
	ep := b.endpoint(key)
	bc := &bindConn{conn: c, ep: ep, done: make(chan struct{})}

	b.mu.Lock()
	old := b.conns[key]
	b.conns[key] = bc
	b.mu.Unlock()
	if old != nil {
		close(old.done)
	}
	go b.readLoop(bc)
}

// RemoveConn unregisters the connection for key. The connection is not closed.
func (b *MultiBind) RemoveConn(key string) {
	b.mu.Lock()
	old := b.conns[key]
	delete(b.conns, key)
	b.mu.Unlock()
	if old != nil {
		close(old.done)
	}
}

// HasConn reports whether a connection is registered for key.
func (b *MultiBind) HasConn(key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.conns[key]
	return ok
}

// Counters returns the bytes received and sent on key's current endpoint.
func (b *MultiBind) Counters(key string) (rx, tx uint64) {
	b.mu.RLock()
	bc := b.conns[key]
	b.mu.RUnlock()
	if bc == nil {
		return 0, 0
	}
	return bc.ep.rx.Load(), bc.ep.tx.Load()
}

func (b *MultiBind) endpoint(key string) *MultiEndpoint {
	ep := &MultiEndpoint{key: key}
	if ap, err := netip.ParseAddrPort(key); err == nil {
		ep.addr = ap
	}
	return ep
}

func (b *MultiBind) readLoop(bc *bindConn) {
	buf := make([]byte, MaxUDPPacketSize)
	for {
		n, err := bc.conn.Read(buf)
		if err != nil {
			select {
			case <-bc.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) || isTerminal(err) {
				b.logger.Debug("bind connection closed", "endpoint", bc.ep.key)
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		select {
		case <-bc.done:
			return
		default:
		}
		bc.ep.rx.Add(uint64(n))
		b.mu.RLock()
		open := b.open
		b.mu.RUnlock()
		if !open {
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		select {
		case b.recv <- inbound{data: pkt, ep: bc.ep}:
		default:
			b.logger.Debug("bind receive queue full, dropping packet")
		}
	}
}

func isTerminal(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range []string{"agent is closed", "agent closed", "closed network connection", "use of closed", "eof"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// Open implements conn.Bind.
func (b *MultiBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		b.open = true
		b.stop = make(chan struct{})
	}
	stop := b.stop
	fn := func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case p := <-b.recv:
			if len(bufs) == 0 {
				return 0, nil
			}
			sizes[0] = copy(bufs[0], p.data)
			eps[0] = p.ep
			return 1, nil
		case <-stop:
			return 0, net.ErrClosed
		}
	}
	return []conn.ReceiveFunc{fn}, 0, nil
}

// Close implements conn.Bind. Registered connections stay registered and
// open; Open may be called again.
func (b *MultiBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		b.open = false
		close(b.stop)
	}
	return nil
}

// Shutdown unregisters every connection. Connections are not closed.
func (b *MultiBind) Shutdown() {
	b.mu.Lock()
	conns := b.conns
	b.conns = make(map[string]*bindConn)
	b.mu.Unlock()
	for _, bc := range conns {
		close(bc.done)
	}
}

// SetMark implements conn.Bind (no-op).
func (b *MultiBind) SetMark(uint32) error { return nil }

// BatchSize implements conn.Bind.
func (b *MultiBind) BatchSize() int { return 1 }

// ParseEndpoint implements conn.Bind. Any key is accepted; packets to a key
// without a registered connection are dropped.
func (b *MultiBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	b.mu.RLock()
	bc := b.conns[s]
	b.mu.RUnlock()
	if bc != nil {
		return bc.ep, nil
	}
	return b.endpoint(s), nil
}

// ErrNoConn is returned by Send when no connection is registered for the
// endpoint.
var ErrNoConn = errors.New("no connection for endpoint")

// Send implements conn.Bind.
func (b *MultiBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	if ep == nil {
		return ErrNoConn
	}
	key := ep.DstToString()
	b.mu.RLock()
	bc := b.conns[key]
	b.mu.RUnlock()
	if bc == nil {
		return ErrNoConn
	}
	for _, buf := range bufs {
		n, err := bc.conn.Write(buf)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		bc.ep.tx.Add(uint64(n))
	}
	return nil
}
