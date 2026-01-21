package wireguard

import (
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateNetTUN_Success(t *testing.T) {
	tests := []struct {
		name           string
		localAddresses []netip.Addr
		dnsServers     []netip.Addr
		mtu            int
	}{
		{
			name:           "IPv4 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            DefaultMTU,
		},
		{
			name:           "IPv6 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("fd00::1")},
			dnsServers:     nil,
			mtu:            DefaultMTU,
		},
		{
			name: "dual-stack",
			localAddresses: []netip.Addr{
				netip.MustParseAddr("10.0.0.1"),
				netip.MustParseAddr("fd00::1"),
			},
			dnsServers: nil,
			mtu:        DefaultMTU,
		},
		{
			name:           "with DNS servers",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers: []netip.Addr{
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("8.8.4.4"),
			},
			mtu: DefaultMTU,
		},
		{
			name:           "minimum MTU",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            MinMTU,
		},
		{
			name:           "maximum MTU",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            MaxMTU,
		},
		{
			name:           "typical WireGuard MTU",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            1420,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunDev, tnet, err := CreateNetTUN(tt.localAddresses, tt.dnsServers, tt.mtu)
			require.NoError(t, err)
			require.NotNil(t, tunDev)
			require.NotNil(t, tnet)

			// Verify accessors
			assert.Equal(t, tt.localAddresses, tnet.LocalAddresses())
			assert.Equal(t, tt.dnsServers, tnet.DNSServers())
			assert.Equal(t, tt.mtu, tnet.MTU())

			// Clean up
			tunDev.Close()
		})
	}
}

func TestCreateNetTUN_Validation_Failure(t *testing.T) {
	tests := []struct {
		name           string
		localAddresses []netip.Addr
		dnsServers     []netip.Addr
		mtu            int
		wantErr        error
		wantErrContain string
	}{
		{
			name:           "no local addresses",
			localAddresses: []netip.Addr{},
			dnsServers:     nil,
			mtu:            DefaultMTU,
			wantErr:        ErrNoLocalAddresses,
		},
		{
			name:           "nil local addresses",
			localAddresses: nil,
			dnsServers:     nil,
			mtu:            DefaultMTU,
			wantErr:        ErrNoLocalAddresses,
		},
		{
			name:           "invalid local address",
			localAddresses: []netip.Addr{netip.Addr{}},
			dnsServers:     nil,
			mtu:            DefaultMTU,
			wantErr:        ErrNoLocalAddresses,
			wantErrContain: "invalid local address",
		},
		{
			name:           "MTU below minimum",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            MinMTU - 1,
			wantErr:        ErrInvalidMTU,
		},
		{
			name:           "MTU above maximum",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            MaxMTU + 1,
			wantErr:        ErrInvalidMTU,
		},
		{
			name:           "zero MTU",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            0,
			wantErr:        ErrInvalidMTU,
		},
		{
			name:           "negative MTU",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     nil,
			mtu:            -1,
			wantErr:        ErrInvalidMTU,
		},
		{
			name:           "invalid DNS server",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			dnsServers:     []netip.Addr{netip.Addr{}},
			mtu:            DefaultMTU,
			wantErrContain: "invalid DNS server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunDev, tnet, err := CreateNetTUN(tt.localAddresses, tt.dnsServers, tt.mtu)
			require.Error(t, err)
			assert.Nil(t, tunDev)
			assert.Nil(t, tnet)

			if tt.wantErr != nil {
				assert.True(t, errors.Is(err, tt.wantErr), "expected error %v, got %v", tt.wantErr, err)
			}
			if tt.wantErrContain != "" {
				assert.Contains(t, err.Error(), tt.wantErrContain)
			}
		})
	}
}

func TestNet_HasIPv4(t *testing.T) {
	tests := []struct {
		name           string
		localAddresses []netip.Addr
		want           bool
	}{
		{
			name:           "IPv4 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			want:           true,
		},
		{
			name:           "IPv6 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("fd00::1")},
			want:           false,
		},
		{
			name: "dual-stack",
			localAddresses: []netip.Addr{
				netip.MustParseAddr("10.0.0.1"),
				netip.MustParseAddr("fd00::1"),
			},
			want: true,
		},
		{
			name: "multiple IPv4",
			localAddresses: []netip.Addr{
				netip.MustParseAddr("10.0.0.1"),
				netip.MustParseAddr("192.168.1.1"),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunDev, tnet, err := CreateNetTUN(tt.localAddresses, nil, DefaultMTU)
			require.NoError(t, err)
			defer tunDev.Close()

			assert.Equal(t, tt.want, tnet.HasIPv4())
		})
	}
}

func TestNet_HasIPv6(t *testing.T) {
	tests := []struct {
		name           string
		localAddresses []netip.Addr
		want           bool
	}{
		{
			name:           "IPv4 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
			want:           false,
		},
		{
			name:           "IPv6 only",
			localAddresses: []netip.Addr{netip.MustParseAddr("fd00::1")},
			want:           true,
		},
		{
			name: "dual-stack",
			localAddresses: []netip.Addr{
				netip.MustParseAddr("10.0.0.1"),
				netip.MustParseAddr("fd00::1"),
			},
			want: true,
		},
		{
			name: "multiple IPv6",
			localAddresses: []netip.Addr{
				netip.MustParseAddr("fd00::1"),
				netip.MustParseAddr("fd00::2"),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunDev, tnet, err := CreateNetTUN(tt.localAddresses, nil, DefaultMTU)
			require.NoError(t, err)
			defer tunDev.Close()

			assert.Equal(t, tt.want, tnet.HasIPv6())
		})
	}
}

func TestNet_TCPIntegration(t *testing.T) {
	// Create a userspace network stack
	tunDev, tnet, err := CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr("10.0.0.1")},
		nil,
		DefaultMTU,
	)
	require.NoError(t, err)
	defer tunDev.Close()

	// Test that we can create a TCP listener
	// Note: This tests the API, not actual connectivity (which requires WireGuard setup)
	listener, err := tnet.ListenTCP(&net.TCPAddr{Port: 8080})
	require.NoError(t, err)
	require.NotNil(t, listener)
	defer listener.Close()

	// Verify that the embedded netstack.Net is accessible
	assert.NotNil(t, tnet.Net, "embedded netstack.Net should be accessible")

	// Note: Actual TCP connectivity tests require a full WireGuard tunnel setup
	// with peers on both ends. The ListenTCP test above verifies the API works.
}
