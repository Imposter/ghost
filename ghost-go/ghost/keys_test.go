package ghost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
)

func TestKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "ghost.json")

	k1, err := LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if k1.PublicKey() == "" {
		t.Fatal("empty public key")
	}

	// Reload: same keys must come back.
	k2, err := LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if k1.PublicKey() != k2.PublicKey() {
		t.Fatalf("public key changed across reload: %s != %s", k1.PublicKey(), k2.PublicKey())
	}

	// Direct load must match too.
	k3, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if k3.PublicKey() != k1.PublicKey() {
		t.Fatal("LoadKeys returned a different key")
	}
}

func TestGenerateKeysUnique(t *testing.T) {
	a, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if a.PublicKey() == b.PublicKey() {
		t.Fatal("two generated keys collided")
	}
}

func TestLoadKeysMissing(t *testing.T) {
	if _, err := LoadKeys(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestKeysFromPrivateKey(t *testing.T) {
	k, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	priv := wireguard.EncodeKey(k.privateKeyBytes())

	got, err := KeysFromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicKey() != k.PublicKey() {
		t.Errorf("public key %q, want %q", got.PublicKey(), k.PublicKey())
	}
	if _, err := KeysFromPrivateKey("not-a-key"); err == nil {
		t.Error("a malformed key was accepted")
	}

	// Config.keys takes the private key over the store, writes nothing, and
	// refuses to be given both.
	dir := t.TempDir()
	cfg := Config{PrivateKey: priv}
	fromCfg, err := cfg.keys()
	if err != nil || fromCfg.PublicKey() != k.PublicKey() {
		t.Fatalf("config keys: %v", err)
	}
	cfg.KeyStorePath = filepath.Join(dir, "keys.json")
	if _, err := cfg.keys(); err == nil {
		t.Error("PrivateKey and KeyStorePath were both accepted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a key file was written: %v", entries)
	}
}
