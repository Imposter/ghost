package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileThenEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.json")
	file := `{"listen": ":9000",
	  "access": {"mode": "api", "authorizer_url": "http://authz", "authorizer_secret": "file-secret-0123456789", "cache_ttl": "5s"},
	  "heartbeat": {"interval": 10, "timeout": "45s"}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"GHOST_LISTEN":      ":9100",
		"GHOST_TURN_URLS":   "turn:a:3478, turn:b:3478",
		"GHOST_TURN_SECRET": "t",
		"GHOST_NETWORKS":    "pool=100.64.0.0/16,lab",
	}
	cfg, err := Load(path, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9100" || cfg.Access.Mode != AccessAPI || cfg.Access.CacheTTL.Std() != 5*time.Second {
		t.Fatalf("merge: %+v", cfg)
	}
	if cfg.Heartbeat.Interval.Std() != 10*time.Second || cfg.Heartbeat.Timeout.Std() != 45*time.Second {
		t.Fatalf("durations: %+v", cfg.Heartbeat)
	}
	if len(cfg.ICE.TURNURLs) != 2 || cfg.ICE.TURNURLs[1] != "turn:b:3478" {
		t.Fatalf("turn urls: %v", cfg.ICE.TURNURLs)
	}
	if len(cfg.Networks) != 2 || cfg.Networks[0].Pool != "100.64.0.0/16" || cfg.Networks[1].Name != "lab" {
		t.Fatalf("networks: %+v", cfg.Networks)
	}
}

func TestValidate(t *testing.T) {
	c := Default()
	c.Access.Mode = AccessAPI
	if err := c.Validate(); err == nil {
		t.Fatal("api mode without an authorizer must fail validation")
	}
	c = Default()
	c.ICE.TURNURLs = []string{"turn:x"}
	if err := c.Validate(); err == nil {
		t.Fatal("TURN without a secret must fail validation")
	}
	bad := map[string]string{"GHOST_HEARTBEAT_INTERVAL": "bogus"}
	if _, err := Load("", func(k string) string { return bad[k] }); err == nil {
		t.Fatal("a bad duration must fail")
	}
}

func TestTrustedProxies(t *testing.T) {
	env := map[string]string{"GHOST_TRUSTED_PROXIES": "172.16.0.0/12, 10.0.0.5"}
	cfg, err := Load("", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "172.16.0.0/12" || cfg.TrustedProxies[1] != "10.0.0.5" {
		t.Fatalf("trusted proxies %q", cfg.TrustedProxies)
	}
	bad := map[string]string{"GHOST_TRUSTED_PROXIES": "the-proxy"}
	if _, err := Load("", func(k string) string { return bad[k] }); err == nil || !strings.Contains(err.Error(), "trusted_proxies") {
		t.Fatalf("a bad trusted proxy: %v", err)
	}
}
