package ice

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultICEConfig(t *testing.T) {
	cfg := DefaultICEConfig()

	assert.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.STUNServers)
	assert.Equal(t, 10*time.Second, cfg.GatherTimeout)
	assert.Equal(t, 30*time.Second, cfg.ConnectionTimeout)
	assert.Equal(t, 15*time.Second, cfg.KeepaliveInterval)
	assert.Contains(t, cfg.CandidateTypes, CandidateTypeHost)
	assert.Contains(t, cfg.CandidateTypes, CandidateTypeSrflx)
	assert.Contains(t, cfg.CandidateTypes, CandidateTypeRelay)
}

func TestICEConfig_Validate_Success(t *testing.T) {
	tests := []struct {
		name   string
		config *ICEConfig
	}{
		{
			name:   "default config",
			config: DefaultICEConfig(),
		},
		{
			name: "with TURN server",
			config: &ICEConfig{
				STUNServers: []string{"stun:stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						URLs:     []string{"turn:turn.example.com:3478"},
						Username: "testuser",
						Password: "testpass",
					},
				},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
		},
		{
			name: "with interface filter",
			config: &ICEConfig{
				STUNServers:       []string{"stun:stun.l.google.com:19302"},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
				InterfaceFilter:   []string{"eth0", "wlan0"},
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

func TestICEConfig_Validate_Failure(t *testing.T) {
	tests := []struct {
		name    string
		config  *ICEConfig
		wantErr string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: "config cannot be nil",
		},
		{
			name: "invalid STUN URL scheme",
			config: &ICEConfig{
				STUNServers:       []string{"http://stun.example.com:3478"},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "invalid STUN server",
		},
		{
			name: "empty STUN host",
			config: &ICEConfig{
				STUNServers:       []string{"stun:"},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "invalid STUN server",
		},
		{
			name: "TURN server without URLs",
			config: &ICEConfig{
				STUNServers: []string{"stun:stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						URLs:     []string{},
						Username: "user",
						Password: "pass",
					},
				},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "no URLs specified",
		},
		{
			name: "TURN server without username",
			config: &ICEConfig{
				STUNServers: []string{"stun:stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						URLs:     []string{"turn:turn.example.com:3478"},
						Username: "",
						Password: "pass",
					},
				},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "username is required",
		},
		{
			name: "TURN server without password",
			config: &ICEConfig{
				STUNServers: []string{"stun:stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						URLs:     []string{"turn:turn.example.com:3478"},
						Username: "user",
						Password: "",
					},
				},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "password is required",
		},
		{
			name: "invalid TURN URL scheme",
			config: &ICEConfig{
				STUNServers: []string{"stun:stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						URLs:     []string{"http://turn.example.com:3478"},
						Username: "user",
						Password: "pass",
					},
				},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "invalid URL",
		},
		{
			name: "zero gather timeout",
			config: &ICEConfig{
				STUNServers:       []string{"stun:stun.example.com:3478"},
				GatherTimeout:     0,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "gather timeout must be positive",
		},
		{
			name: "negative connection timeout",
			config: &ICEConfig{
				STUNServers:       []string{"stun:stun.example.com:3478"},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: -1 * time.Second,
				KeepaliveInterval: 15 * time.Second,
			},
			wantErr: "connection timeout must be positive",
		},
		{
			name: "zero keepalive interval",
			config: &ICEConfig{
				STUNServers:       []string{"stun:stun.example.com:3478"},
				GatherTimeout:     10 * time.Second,
				ConnectionTimeout: 30 * time.Second,
				KeepaliveInterval: 0,
			},
			wantErr: "keepalive interval must be positive",
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

func TestValidateServerURL(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		scheme    string
		wantErr   bool
	}{
		{
			name:      "valid STUN URL",
			serverURL: "stun:stun.l.google.com:19302",
			scheme:    "stun",
			wantErr:   false,
		},
		{
			name:      "valid TURN URL",
			serverURL: "turn:turn.example.com:3478",
			scheme:    "turn",
			wantErr:   false,
		},
		{
			name:      "empty URL",
			serverURL: "",
			scheme:    "stun",
			wantErr:   true,
		},
		{
			name:      "wrong scheme",
			serverURL: "http://stun.example.com:3478",
			scheme:    "stun",
			wantErr:   true,
		},
		{
			name:      "missing host",
			serverURL: "stun:",
			scheme:    "stun",
			wantErr:   true,
		},
		{
			name:      "invalid URL format",
			serverURL: "not a url",
			scheme:    "stun",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerURL(tt.serverURL, tt.scheme)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
