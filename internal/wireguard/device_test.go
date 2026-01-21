package wireguard

import (
	"io"
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/tun"
)

// mockTUNDevice implements tun.Device for testing
type mockTUNDevice struct {
	name   string
	mtu    int
	events chan tun.Event
	closed bool
}

func newMockTUNDevice(name string, mtu int) *mockTUNDevice {
	m := &mockTUNDevice{
		name:   name,
		mtu:    mtu,
		events: make(chan tun.Event, 1),
	}
	// Send an Up event immediately so WireGuard doesn't wait
	m.events <- tun.EventUp
	return m
}

func (m *mockTUNDevice) File() *os.File { return nil }
func (m *mockTUNDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	// Block until closed
	if m.closed {
		return 0, io.EOF
	}
	time.Sleep(10 * time.Millisecond)
	return 0, nil
}
func (m *mockTUNDevice) Write(bufs [][]byte, offset int) (int, error) { return 1, nil }
func (m *mockTUNDevice) Flush() error                                 { return nil }
func (m *mockTUNDevice) MTU() (int, error)                            { return m.mtu, nil }
func (m *mockTUNDevice) Name() (string, error)                        { return m.name, nil }
func (m *mockTUNDevice) Events() <-chan tun.Event                     { return m.events }
func (m *mockTUNDevice) Close() error {
	if !m.closed {
		m.closed = true
		close(m.events)
	}
	return nil
}
func (m *mockTUNDevice) BatchSize() int { return 1 }

// mockBind implements conn.Bind for testing
type mockBind struct {
	closed bool
}

func (m *mockBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	return []conn.ReceiveFunc{func([][]byte, []int, []conn.Endpoint) (int, error) {
		return 0, nil
	}}, port, nil
}
func (m *mockBind) Close() error {
	m.closed = true
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

func newMockDevice(t *testing.T) (*Device, *mockTUNDevice, *mockBind) {
	tunDev := newMockTUNDevice("test0", 1280)

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

	device, err := NewDevice(tunDev, bind, config, logger)
	require.NoError(t, err)

	return device, tunDev, bind
}

func TestNewDevice_Success(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	assert.NotNil(t, device)
	assert.False(t, device.IsUp())
}

func TestNewDevice_Validation(t *testing.T) {
	tunDev := newMockTUNDevice("test0", 1280)
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
			name:    "nil TUN device",
			tunDev:  nil,
			bind:    bind,
			config:  validConfig,
			wantErr: "TUN device cannot be nil",
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
			_, err := NewDevice(tt.tunDev, tt.bind, tt.config, logger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDevice_Configure(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	err = device.Configure(privateKey)
	assert.NoError(t, err)
}

func TestDevice_Configure_InvalidKey(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	err := device.Configure(make([]byte, 16)) // Wrong size
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid private key")
}

func TestDevice_AddPeer(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = device.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = device.AddPeer(peerConfig)
	assert.NoError(t, err)

	peers := device.GetPeers()
	assert.Len(t, peers, 1)
}

func TestDevice_AddPeer_Duplicate(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = device.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = device.AddPeer(peerConfig)
	require.NoError(t, err)

	// Try to add again
	err = device.AddPeer(peerConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "peer already exists")
}

func TestDevice_RemovePeer(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	err = device.Configure(privateKey)
	require.NoError(t, err)

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	peerConfig := &PeerConfig{
		PublicKey:  peerPubKey,
		AllowedIPs: []string{"10.0.0.2/32"},
	}

	err = device.AddPeer(peerConfig)
	require.NoError(t, err)

	err = device.RemovePeer(peerPubKey)
	assert.NoError(t, err)

	peers := device.GetPeers()
	assert.Len(t, peers, 0)
}

func TestDevice_RemovePeer_NotFound(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	peerPrivKey, err := GeneratePrivateKey()
	require.NoError(t, err)
	peerPubKey, err := GetPublicKey(peerPrivKey)
	require.NoError(t, err)

	err = device.RemovePeer(peerPubKey)
	assert.ErrorIs(t, err, ErrPeerNotFound)
}

func TestDevice_UpDown(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	assert.False(t, device.IsUp())

	err := device.Up()
	assert.NoError(t, err)
	assert.True(t, device.IsUp())

	err = device.Down()
	assert.NoError(t, err)
	assert.False(t, device.IsUp())

	// Up again
	err = device.Up()
	assert.NoError(t, err)
	assert.True(t, device.IsUp())
}

func TestDevice_UpDown_Idempotent(t *testing.T) {
	device, _, _ := newMockDevice(t)
	defer device.Close()

	// Up twice
	err := device.Up()
	assert.NoError(t, err)
	err = device.Up()
	assert.NoError(t, err)

	// Down twice
	err = device.Down()
	assert.NoError(t, err)
	err = device.Down()
	assert.NoError(t, err)
}

func TestDevice_GetTUNName(t *testing.T) {
	device, tunDev, _ := newMockDevice(t)
	defer device.Close()

	name, err := device.GetTUNName()
	assert.NoError(t, err)
	assert.Equal(t, tunDev.name, name)
}

func TestDevice_GetMTU(t *testing.T) {
	device, tunDev, _ := newMockDevice(t)
	defer device.Close()

	mtu, err := device.GetMTU()
	assert.NoError(t, err)
	assert.Equal(t, tunDev.mtu, mtu)
}

func TestDevice_Close(t *testing.T) {
	device, _, bind := newMockDevice(t)

	err := device.Close()
	assert.NoError(t, err)
	assert.True(t, bind.closed)

	// Close again should be no-op
	err = device.Close()
	assert.NoError(t, err)
}

func TestDevice_OperationsAfterClose(t *testing.T) {
	device, _, _ := newMockDevice(t)
	err := device.Close()
	require.NoError(t, err)

	// All operations should return ErrDeviceClosed
	err = device.Up()
	assert.ErrorIs(t, err, ErrDeviceClosed)

	err = device.Down()
	assert.ErrorIs(t, err, ErrDeviceClosed)

	_, err = device.GetTUNName()
	assert.ErrorIs(t, err, ErrDeviceClosed)

	_, err = device.GetMTU()
	assert.ErrorIs(t, err, ErrDeviceClosed)
}
