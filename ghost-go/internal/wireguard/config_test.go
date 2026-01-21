package wireguard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultWireGuardConfig(t *testing.T) {
	cfg := DefaultWireGuardConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, 0, cfg.ListenPort)
	assert.Equal(t, 1280, cfg.MTU)
	assert.Equal(t, 25*time.Second, cfg.PersistentKeepalive)
}

func TestWireGuardConfig_Validate_Success(t *testing.T) {
	privateKey := make([]byte, 32)
	for i := range privateKey {
		privateKey[i] = byte(i)
	}

	tests := []struct {
		name   string
		config *WireGuardConfig
	}{
		{
			name: "valid config",
			config: &WireGuardConfig{
				PrivateKey:          privateKey,
				ListenPort:          51820,
				MTU:                 1420,
				PersistentKeepalive: 25 * time.Second,
			},
		},
		{
			name: "auto-assign port",
			config: &WireGuardConfig{
				PrivateKey:          privateKey,
				ListenPort:          0,
				MTU:                 1280,
				PersistentKeepalive: 25 * time.Second,
			},
		},
		{
			name: "zero keepalive",
			config: &WireGuardConfig{
				PrivateKey:          privateKey,
				ListenPort:          51820,
				MTU:                 1280,
				PersistentKeepalive: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestWireGuardConfig_Validate_Failure(t *testing.T) {
	validKey := make([]byte, 32)

	tests := []struct {
		name    string
		config  *WireGuardConfig
		wantErr string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: "config cannot be nil",
		},
		{
			name: "invalid private key size",
			config: &WireGuardConfig{
				PrivateKey: make([]byte, 16),
				ListenPort: 51820,
				MTU:        1280,
			},
			wantErr: "private key must be 32 bytes",
		},
		{
			name: "negative listen port",
			config: &WireGuardConfig{
				PrivateKey: validKey,
				ListenPort: -1,
				MTU:        1280,
			},
			wantErr: "invalid listen port",
		},
		{
			name: "listen port too large",
			config: &WireGuardConfig{
				PrivateKey: validKey,
				ListenPort: 65536,
				MTU:        1280,
			},
			wantErr: "invalid listen port",
		},
		{
			name: "zero MTU",
			config: &WireGuardConfig{
				PrivateKey: validKey,
				ListenPort: 51820,
				MTU:        0,
			},
			wantErr: "MTU must be positive",
		},
		{
			name: "MTU too small",
			config: &WireGuardConfig{
				PrivateKey: validKey,
				ListenPort: 51820,
				MTU:        500,
			},
			wantErr: "MTU too small",
		},
		{
			name: "MTU too large",
			config: &WireGuardConfig{
				PrivateKey: validKey,
				ListenPort: 51820,
				MTU:        70000,
			},
			wantErr: "MTU too large",
		},
		{
			name: "negative keepalive",
			config: &WireGuardConfig{
				PrivateKey:          validKey,
				ListenPort:          51820,
				MTU:                 1280,
				PersistentKeepalive: -1 * time.Second,
			},
			wantErr: "persistent keepalive cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPeerConfig_Validate_Success(t *testing.T) {
	publicKey := make([]byte, 32)
	for i := range publicKey {
		publicKey[i] = byte(i + 1)
	}

	tests := []struct {
		name   string
		config *PeerConfig
	}{
		{
			name: "valid peer config",
			config: &PeerConfig{
				PublicKey:           publicKey,
				AllowedIPs:          []string{"10.0.0.2/32"},
				Endpoint:            "example.com:51820",
				PersistentKeepalive: 25 * time.Second,
			},
		},
		{
			name: "IPv6 allowed IPs",
			config: &PeerConfig{
				PublicKey:  publicKey,
				AllowedIPs: []string{"fd00::2/128"},
			},
		},
		{
			name: "multiple allowed IPs",
			config: &PeerConfig{
				PublicKey:  publicKey,
				AllowedIPs: []string{"10.0.0.2/32", "fd00::2/128"},
			},
		},
		{
			name: "no endpoint",
			config: &PeerConfig{
				PublicKey:  publicKey,
				AllowedIPs: []string{"10.0.0.2/32"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestPeerConfig_Validate_Failure(t *testing.T) {
	validKey := make([]byte, 32)

	tests := []struct {
		name    string
		config  *PeerConfig
		wantErr string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: "peer config cannot be nil",
		},
		{
			name: "invalid public key size",
			config: &PeerConfig{
				PublicKey:  make([]byte, 16),
				AllowedIPs: []string{"10.0.0.2/32"},
			},
			wantErr: "public key must be 32 bytes",
		},
		{
			name: "no allowed IPs",
			config: &PeerConfig{
				PublicKey:  validKey,
				AllowedIPs: []string{},
			},
			wantErr: "at least one allowed IP must be specified",
		},
		{
			name: "invalid allowed IP format",
			config: &PeerConfig{
				PublicKey:  validKey,
				AllowedIPs: []string{"not-an-ip"},
			},
			wantErr: "invalid allowed IP",
		},
		{
			name: "invalid CIDR",
			config: &PeerConfig{
				PublicKey:  validKey,
				AllowedIPs: []string{"10.0.0.2"},
			},
			wantErr: "invalid allowed IP",
		},
		{
			name: "invalid endpoint format",
			config: &PeerConfig{
				PublicKey:  validKey,
				AllowedIPs: []string{"10.0.0.2/32"},
				Endpoint:   "invalid",
			},
			wantErr: "invalid endpoint format",
		},
		{
			name: "endpoint missing port",
			config: &PeerConfig{
				PublicKey:  validKey,
				AllowedIPs: []string{"10.0.0.2/32"},
				Endpoint:   "example.com",
			},
			wantErr: "invalid endpoint format",
		},
		{
			name: "negative keepalive",
			config: &PeerConfig{
				PublicKey:           validKey,
				AllowedIPs:          []string{"10.0.0.2/32"},
				PersistentKeepalive: -1 * time.Second,
			},
			wantErr: "persistent keepalive cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
