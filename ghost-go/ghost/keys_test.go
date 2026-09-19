package ghost

import (
	"path/filepath"
	"testing"
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
