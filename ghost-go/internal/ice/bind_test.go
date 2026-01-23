package ice

import (
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ghost-go/internal/testutil"
)

// =============================================================================
// MockConn for testing
// =============================================================================

// mockConn implements net.Conn for testing purposes
type mockConn struct {
	localAddr  net.Addr
	remoteAddr net.Addr
	closed     bool
	readChan   chan []byte
	writeBuf   [][]byte
	mu         sync.Mutex
}

func newMockConn(local, remote string) *mockConn {
	localAddr, _ := net.ResolveUDPAddr("udp", local)
	remoteAddr, _ := net.ResolveUDPAddr("udp", remote)
	return &mockConn{
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
		readChan:   make(chan []byte, 10),
		writeBuf:   make([][]byte, 0),
	}
}

func (c *mockConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	c.mu.Unlock()

	select {
	case data := <-c.readChan:
		n := copy(b, data)
		return n, nil
	case <-time.After(100 * time.Millisecond):
		return 0, nil
	}
}

func (c *mockConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	buf := make([]byte, len(b))
	copy(buf, b)
	c.writeBuf = append(c.writeBuf, buf)
	return len(b), nil
}

func (c *mockConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *mockConn) LocalAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil // Simulate closed connection returning nil
	}
	return c.localAddr
}

func (c *mockConn) RemoteAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil // Simulate closed connection returning nil
	}
	return c.remoteAddr
}

func (c *mockConn) SetDeadline(t time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func (c *mockConn) injectPacket(data []byte) {
	c.readChan <- data
}

func (c *mockConn) getWrittenData() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeBuf
}

// =============================================================================
// NewICEBind Tests
// =============================================================================

func TestNewICEBind_NilConnection(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	bind := NewICEBind(nil, logger)
	assert.Nil(t, bind, "NewICEBind should return nil for nil connection")
}

func TestNewICEBind_ClosedConnection(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	conn.Close() // Close the connection before creating bind

	bind := NewICEBind(conn, logger)
	assert.Nil(t, bind, "NewICEBind should return nil for closed connection")
}

func TestNewICEBind_ValidConnection(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind, "NewICEBind should return valid bind")
	defer bind.Close()

	assert.NotNil(t, bind.endpoint, "endpoint should be set")
	assert.Equal(t, conn, bind.conn, "connection should be stored")
}

func TestNewICEBind_NilLogger(t *testing.T) {
	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	// Should not panic with nil logger
	bind := NewICEBind(conn, nil)
	require.NotNil(t, bind, "should create bind with default logger")
	defer bind.Close()
}

// =============================================================================
// Open Tests
// =============================================================================

func TestICEBind_Open(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	receiveFuncs, port, err := bind.Open(0)
	require.NoError(t, err, "Open should succeed")
	assert.NotEmpty(t, receiveFuncs, "should return receive functions")
	assert.Equal(t, uint16(5000), port, "should return local port")
}

func TestICEBind_OpenIdempotent(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	// First open
	funcs1, port1, err1 := bind.Open(0)
	require.NoError(t, err1)

	// Second open should also succeed
	funcs2, port2, err2 := bind.Open(0)
	require.NoError(t, err2)

	assert.Equal(t, len(funcs1), len(funcs2), "should return same number of receive funcs")
	assert.Equal(t, port1, port2, "should return same port")
}

// =============================================================================
// Close Tests
// =============================================================================

func TestICEBind_Close(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)

	// Open first
	_, _, err := bind.Open(0)
	require.NoError(t, err)

	// Close
	err = bind.Close()
	assert.NoError(t, err, "Close should succeed")

	// Verify closed state
	bind.mu.RLock()
	open := bind.open
	bind.mu.RUnlock()

	assert.False(t, open, "should be marked as closed")
}

func TestICEBind_CloseIdempotent(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)

	_, _, _ = bind.Open(0)

	// Multiple closes should not error
	for i := 0; i < 5; i++ {
		err := bind.Close()
		assert.NoError(t, err, "Close iteration %d should succeed", i)
	}
}

func TestICEBind_CloseDoesNotCloseUnderlyingConnection(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)

	_, _, _ = bind.Open(0)
	bind.Close()

	// Connection should still be usable (not closed by bind)
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()

	assert.False(t, closed, "underlying connection should not be closed by bind.Close()")

	// Clean up
	conn.Close()
}

// =============================================================================
// Send Tests
// =============================================================================

func TestICEBind_Send(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	_, _, _ = bind.Open(0)

	// Send some data
	testData := [][]byte{
		[]byte("hello"),
		[]byte("world"),
	}

	err := bind.Send(testData, bind.endpoint)
	assert.NoError(t, err, "Send should succeed")

	// Verify data was written
	written := conn.getWrittenData()
	assert.Len(t, written, 2, "should have written 2 packets")
	assert.Equal(t, []byte("hello"), written[0])
	assert.Equal(t, []byte("world"), written[1])
}

func TestICEBind_SendWithNilEndpoint(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	_, _, _ = bind.Open(0)

	// Send with nil endpoint should work (ICE is point-to-point)
	testData := [][]byte{[]byte("test")}
	err := bind.Send(testData, nil)
	assert.NoError(t, err)
}

// =============================================================================
// BatchSize Tests
// =============================================================================

func TestICEBind_BatchSize(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	assert.Equal(t, 1, bind.BatchSize(), "BatchSize should be 1")
}

// =============================================================================
// ParseEndpoint Tests
// =============================================================================

func TestICEBind_ParseEndpoint(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "192.168.1.100:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	t.Run("empty string", func(t *testing.T) {
		ep, err := bind.ParseEndpoint("")
		assert.NoError(t, err)
		assert.Equal(t, bind.endpoint, ep)
	})

	t.Run("matching endpoint", func(t *testing.T) {
		ep, err := bind.ParseEndpoint("192.168.1.100:5001")
		assert.NoError(t, err)
		assert.NotNil(t, ep)
	})

	t.Run("valid IP:port", func(t *testing.T) {
		ep, err := bind.ParseEndpoint("10.0.0.1:1234")
		assert.NoError(t, err)
		assert.NotNil(t, ep)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := bind.ParseEndpoint("not-an-endpoint")
		assert.Error(t, err)
	})
}

// =============================================================================
// SetMark Tests
// =============================================================================

func TestICEBind_SetMark(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	// SetMark is a no-op but should not error
	err := bind.SetMark(12345)
	assert.NoError(t, err)
}

// =============================================================================
// Receive Tests
// =============================================================================

func TestICEBind_ReceiveFuncCreation(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	receiveFuncs, _, err := bind.Open(0)
	require.NoError(t, err)
	require.Len(t, receiveFuncs, 1, "should return exactly one receive function")

	// Verify receive function is not nil
	require.NotNil(t, receiveFuncs[0], "receive function should not be nil")
}

// =============================================================================
// Resource Leak Tests
// =============================================================================

func TestICEBind_GoroutineLeak(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Create and close multiple binds
	for i := 0; i < 10; i++ {
		conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
		bind := NewICEBind(conn, logger)
		if bind != nil {
			_, _, _ = bind.Open(0)
			bind.Close()
		}
		conn.Close()
	}

	// Wait for goroutines to clean up
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	leaked := finalGoroutines - initialGoroutines

	if leaked > 5 {
		t.Errorf("potential goroutine leak: started with %d, ended with %d (leaked %d)",
			initialGoroutines, finalGoroutines, leaked)
	}
}

func TestICEBind_OpenCloseReopen(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	// Open, close, reopen cycle
	for i := 0; i < 5; i++ {
		_, port, err := bind.Open(0)
		require.NoError(t, err, "Open iteration %d should succeed", i)
		assert.Equal(t, uint16(5000), port)

		err = bind.Close()
		require.NoError(t, err, "Close iteration %d should succeed", i)
	}
}

// =============================================================================
// ICEEndpoint Tests
// =============================================================================

func TestICEEndpoint_NilAddress(t *testing.T) {
	// Should handle nil address gracefully
	ep := NewICEEndpoint(nil)
	assert.NotNil(t, ep, "should create endpoint even with nil address")
}

func TestICEEndpoint_DstToString(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:5001")
	ep := NewICEEndpoint(addr)

	dst := ep.DstToString()
	assert.Equal(t, "192.168.1.100:5001", dst)
}

func TestICEEndpoint_SrcToString(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:5001")
	ep := NewICEEndpoint(addr)

	// For ICE, source is same as destination
	src := ep.SrcToString()
	assert.Equal(t, "192.168.1.100:5001", src)
}

func TestICEEndpoint_DstIP(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:5001")
	ep := NewICEEndpoint(addr)

	ip := ep.DstIP()
	assert.NotNil(t, ip)
	assert.True(t, ip.IsValid())
}

func TestICEEndpoint_DstToBytes(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:5001")
	ep := NewICEEndpoint(addr)

	bytes := ep.DstToBytes()
	assert.NotEmpty(t, bytes)
	assert.Equal(t, "192.168.1.100:5001", string(bytes))
}

func TestICEEndpoint_SrcIP(t *testing.T) {
	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:5001")
	ep := NewICEEndpoint(addr)

	// Should return zero IP (no source info for ICE)
	ip := ep.SrcIP()
	assert.NotNil(t, ip)
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestICEBind_ConcurrentSend(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	_, _, _ = bind.Open(0)

	var wg sync.WaitGroup
	const numGoroutines = 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				data := [][]byte{[]byte("test")}
				_ = bind.Send(data, bind.endpoint)
			}
		}(i)
	}

	wg.Wait()
}

func TestICEBind_ConcurrentOpenClose(t *testing.T) {
	logger := testutil.NewQuietTestLogger(t)

	conn := newMockConn("127.0.0.1:5000", "127.0.0.1:5001")
	defer conn.Close()

	bind := NewICEBind(conn, logger)
	require.NotNil(t, bind)
	defer bind.Close()

	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _, _ = bind.Open(0)
				_ = bind.Close()
			}
		}()
	}

	wg.Wait()
}
