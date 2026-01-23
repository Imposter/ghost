package mobile

import (
	"net"
	"sync"
	"sync/atomic"
)

// connPool manages active connections created through the tunnel.
// This allows the mobile API to track connections by ID for explicit close operations.
//
// # Thread Safety
//
// All methods are thread-safe and can be called from any goroutine.
//
// # Resource Management
//
// connPool owns the connections it manages. When closeAll() is called,
// all connections are closed and the pool is marked as closed. Any new
// connections added after closeAll() are immediately closed.
type connPool struct {
	mu     sync.RWMutex
	conns  map[int64]net.Conn
	nextID int64
	closed bool
}

// newConnPool creates a new connection pool.
func newConnPool() *connPool {
	return &connPool{
		conns: make(map[int64]net.Conn),
	}
}

// add adds a connection to the pool and returns its ID.
func (p *connPool) add(conn net.Conn) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		conn.Close()
		return -1
	}

	id := atomic.AddInt64(&p.nextID, 1)
	p.conns[id] = conn
	return id
}

// get retrieves a connection by ID.
func (p *connPool) get(id int64) (net.Conn, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	conn, ok := p.conns[id]
	return conn, ok
}

// remove removes and closes a connection by ID.
func (p *connPool) remove(id int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn, ok := p.conns[id]
	if !ok {
		return ErrConnectionNotFound
	}

	delete(p.conns, id)
	return conn.Close()
}

// closeAll closes all connections and clears the pool.
func (p *connPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	for id, conn := range p.conns {
		conn.Close()
		delete(p.conns, id)
	}
}

// count returns the number of active connections.
func (p *connPool) count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.conns)
}
