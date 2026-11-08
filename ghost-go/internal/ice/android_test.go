//go:build android

package ice

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Imposter/ghost/ghost-go/internal/testutil"
)

// TestAndroidGathersHostCandidates lists the device's interfaces the way a
// node does. From Android 11 an app may not ask the kernel for its links
// (RTM_GETLINK), so net.Interfaces fails there, and pion lists them through
// github.com/wlynxg/anet instead. Run it in an app's sandbox (run-as) to test
// that path: adb shell itself is still allowed RTM_GETLINK. It binds an
// ephemeral UDP port on the device's own addresses and sends nothing.
func TestAndroidGathersHostCandidates(t *testing.T) {
	_, err := net.Interfaces()
	t.Logf("net.Interfaces (unused, for comparison): %v", err)

	cfg := DefaultICEConfig()
	cfg.STUNServers = nil
	cfg.CandidateTypes = []CandidateType{CandidateTypeHost}

	agent, err := NewAgent(cfg, testutil.NewTestLogger(t))
	require.NoError(t, err, "listing interfaces")
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cands, err := agent.GatherCandidates(ctx)
	require.NoError(t, err)

	var hosts []string
	for c := range cands {
		t.Logf("candidate %s %s:%d", c.Type, c.Address, c.Port)
		if ip := net.ParseIP(c.Address); c.Type == CandidateTypeHost && ip != nil && !ip.IsLoopback() {
			hosts = append(hosts, c.Address)
		}
	}
	require.NotEmpty(t, hosts, "no host candidate on a non-loopback interface")
}
