package wireguard

import (
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/tun"
)

// mockTUN implements tun.Device for testing
type mockTUN struct {
	name   string
	mtu    int
	events chan tun.Event
	closed atomic.Bool
}

func newMockTUN(name string, mtu int) *mockTUN {
	m := &mockTUN{
		name:   name,
		mtu:    mtu,
		events: make(chan tun.Event, 1),
	}
	// Send an Up event immediately so WireGuard doesn't wait
	m.events <- tun.EventUp
	return m
}

func (m *mockTUN) File() *os.File { return nil }
func (m *mockTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	// Block until closed
	if m.closed.Load() {
		return 0, io.EOF
	}
	time.Sleep(10 * time.Millisecond)
	return 0, nil
}
func (m *mockTUN) Write(bufs [][]byte, offset int) (int, error) { return 1, nil }
func (m *mockTUN) Flush() error                                 { return nil }
func (m *mockTUN) MTU() (int, error)                            { return m.mtu, nil }
func (m *mockTUN) Name() (string, error)                        { return m.name, nil }
func (m *mockTUN) Events() <-chan tun.Event                     { return m.events }
func (m *mockTUN) Close() error {
	if m.closed.CompareAndSwap(false, true) {
		close(m.events)
	}
	return nil
}
func (m *mockTUN) BatchSize() int { return 1 }

// mockBind implements conn.Bind for testing
type mockBind struct {
	closed atomic.Bool
}

func (m *mockBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	return []conn.ReceiveFunc{func([][]byte, []int, []conn.Endpoint) (int, error) {
		// Check if closed - return error to signal shutdown
		if m.closed.Load() {
			return 0, net.ErrClosed
		}
		// Sleep briefly to prevent spinning, allows checking closed flag periodically
		time.Sleep(10 * time.Millisecond)
		return 0, nil
	}}, port, nil
}
func (m *mockBind) Close() error {
	m.closed.Store(true)
	return nil
}
func (m *mockBind) SetMark(mark uint32) error          { return nil }
func (m *mockBind) Send([][]byte, conn.Endpoint) error { return nil }
func (m *mockBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	return &mockEndpoint{addr: s}, nil
}
func (m *mockBind) BatchSize() int { return 1 }

type mockEndpoint struct {
	addr string
}

func (e *mockEndpoint) ClearSrc()           {}
func (e *mockEndpoint) SrcToString() string { return e.addr }
func (e *mockEndpoint) DstToString() string { return e.addr }
func (e *mockEndpoint) DstToBytes() []byte  { return []byte(e.addr) }
func (e *mockEndpoint) DstIP() netip.Addr   { return netip.Addr{} }
func (e *mockEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }

func newMockTunnel(t *testing.T) (*Tunnel, *mockTUN, *mockBind) {
	tunDev := newMockTUN("test0", 1280)

	bind := &mockBind{}

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	config := &WireGuardConfig{
		PrivateKey:          privateKey,
		ListenPort:          0,
		MTU:                 1280,
		PersistentKeepalive: 25 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tunnel, err := NewTunnel(tunDev, bind, config, logger)
	require.NoError(t, err)

	return tunnel, tunDev, bind
}

func TestNewTunnel_Success(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	assert.NotNil(t, tunnel)
	assert.False(t, tunnel.IsUp())
}

func TestNewTunnel_Validation(t *testing.T) {
	tunDev := newMockTUN("test0", 1280)
	defer tunDev.Close()

	bind := &mockBind{}
	validConfig := DefaultWireGuardConfig()
	validConfig.PrivateKey, _ = GeneratePrivateKey()
	logger := slog.Default()

	tests := []struct {
		name    string
		tunDev  tun.Device
		bind    conn.Bind
		config  *WireGuardConfig
		wantErr string
	}{
		{
			name:    "nil TUN interface",
			tunDev:  nil,
			bind:    bind,
			config:  validConfig,
			wantErr: "TUN interface cannot be nil",
		},
		{
			name:    "nil bind",
			tunDev:  tunDev,
			bind:    nil,
			config:  validConfig,
			wantErr: "bind cannot be nil",
		},
		{
			name:    "nil config",
			tunDev:  tunDev,
			bind:    bind,
			config:  nil,
			wantErr: "config cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTunnel(tt.tunDev, tt.bind, tt.config, logger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestTunnel_Configure(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	err = tunnel.Configure(privateKey)
	assert.NoError(t, err)
}

func TestTunnel_Configure_InvalidKey(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	err := tunnel.Configure(make([]byte, 16)) // Wrong size
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid private key")
}

func TestTunnel_AddPeer(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = tunnel.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = tunnel.AddPeer(peerConfig)
	assert.NoError(t, err)

	peers := tunnel.GetPeers()
	assert.Len(t, peers, 1)
}

func TestTunnel_AddPeer_Duplicate(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = tunnel.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = tunnel.AddPeer(peerConfig)
	require.NoError(t, err)

	// Try to add again
	err = tunnel.AddPeer(peerConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "peer already exists")
}

func TestTunnel_RemovePeer(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = tunnel.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = tunnel.AddPeer(peerConfig)
	require.NoError(t, err)

	err = tunnel.RemovePeer(peerPubKey)
	assert.NoError(t, err)

	peers := tunnel.GetPeers()
	assert.Len(t, peers, 0)
}

func TestTunnel_RemovePeer_NotFound(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	err = tunnel.RemovePeer(peerPubKey)
	assert.ErrorIs(t, err, ErrPeerNotFound)
}

func TestTunnel_UpDown(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	assert.False(t, tunnel.IsUp())

	err := tunnel.Up()
	assert.NoError(t, err)
	assert.True(t, tunnel.IsUp())

	err = tunnel.Down()
	assert.NoError(t, err)
	assert.False(t, tunnel.IsUp())

	// Up again
	err = tunnel.Up()
	assert.NoError(t, err)
	assert.True(t, tunnel.IsUp())
}

func TestTunnel_UpDown_Idempotent(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	defer tunnel.Close()

	// Up twice
	err := tunnel.Up()
	assert.NoError(t, err)
	err = tunnel.Up()
	assert.NoError(t, err)

	// Down twice
	err = tunnel.Down()
	assert.NoError(t, err)
	err = tunnel.Down()
	assert.NoError(t, err)
}

func TestTunnel_GetTUNName(t *testing.T) {
	tunnel, tunDev, _ := newMockTunnel(t)
	defer tunnel.Close()

	name, err := tunnel.GetTUNName()
	assert.NoError(t, err)
	assert.Equal(t, tunDev.name, name)
}

func TestTunnel_GetMTU(t *testing.T) {
	tunnel, tunDev, _ := newMockTunnel(t)
	defer tunnel.Close()

	mtu, err := tunnel.GetMTU()
	assert.NoError(t, err)
	assert.Equal(t, tunDev.mtu, mtu)
}

func TestTunnel_Close(t *testing.T) {
	tunnel, _, bind := newMockTunnel(t)

	err := tunnel.Close()
	assert.NoError(t, err)
	assert.True(t, bind.closed.Load())

	// Close again should be no-op
	err = tunnel.Close()
	assert.NoError(t, err)
}

func TestTunnel_OperationsAfterClose(t *testing.T) {
	tunnel, _, _ := newMockTunnel(t)
	err := tunnel.Close()
	require.NoError(t, err)

	// All operations should return ErrTunnelClosed
	err = tunnel.Up()
	assert.ErrorIs(t, err, ErrTunnelClosed)

	err = tunnel.Down()
	assert.ErrorIs(t, err, ErrTunnelClosed)

	_, err = tunnel.GetTUNName()
	assert.ErrorIs(t, err, ErrTunnelClosed)

	_, err = tunnel.GetMTU()
	assert.ErrorIs(t, err, ErrTunnelClosed)
}
