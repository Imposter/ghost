package ice

import (
	"net"
	"testing"

	pion "github.com/pion/ice/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentConfigDisablesMulticastDNS: the epic's promise is that a volunteer's
// machine opens no inbound port. Pion's default mDNS mode joins 224.0.0.251 /
// ff02::fb and binds UDP 5353 on every interface; the only remote peer here is
// a hub, which never offers a .local candidate, so it buys nothing and is an
// inbound surface on a well-known port.
func TestAgentConfigDisablesMulticastDNS(t *testing.T) {
	for name, cfg := range map[string]*ICEConfig{
		"default": DefaultICEConfig(),
		"test":    TestICEConfig(0),
	} {
		t.Run(name, func(t *testing.T) {
			ac, err := agentConfig(cfg)
			require.NoError(t, err)
			assert.Equal(t, pion.MulticastDNSModeDisabled, ac.MulticastDNSMode, "mDNS must never be gathered or answered")
		})
	}
}

// TestAgentConfigLoopbackIsTestOnly: loopback candidates are useless to a
// remote peer, so what volunteers run must not gather them. Only a config that
// asks for them (the test ones) gets them.
func TestAgentConfigLoopbackIsTestOnly(t *testing.T) {
	def := DefaultICEConfig()
	assert.False(t, def.IncludeLoopback, "the default config gathers no loopback candidate")
	ac, err := agentConfig(def)
	require.NoError(t, err)
	assert.False(t, ac.IncludeLoopback)

	// The loopback tests keep working because they ask for it.
	tc := TestICEConfig(0)
	assert.True(t, tc.IncludeLoopback, "the test config gathers loopback candidates")
	ac, err = agentConfig(tc)
	require.NoError(t, err)
	assert.True(t, ac.IncludeLoopback)

	def.IncludeLoopback = true
	ac, err = agentConfig(def)
	require.NoError(t, err)
	assert.True(t, ac.IncludeLoopback)
}

// TestAgentGathersNoLoopbackCandidateByDefault: the knob is not cosmetic. An
// agent built from the default config offers no 127.0.0.1 candidate, on a
// config restricted to loopback addresses, which therefore gathers nothing at
// all rather than binding a routable socket.
func TestAgentGathersNoLoopbackCandidateByDefault(t *testing.T) {
	cfg := TestICEConfig(0) // host candidates, loopback IPs only, no STUN/TURN
	cfg.IncludeLoopback = false

	agent, err := NewAgent(cfg, nil)
	require.NoError(t, err)
	defer agent.Close()

	ch, err := agent.GatherCandidates(t.Context())
	require.NoError(t, err)
	for c := range ch {
		if ip := net.ParseIP(c.Address); ip != nil && ip.IsLoopback() {
			t.Errorf("gathered a loopback candidate without IncludeLoopback: %s", c)
		}
	}
}
