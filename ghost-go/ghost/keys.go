package ghost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Imposter/ghost/ghost-go/internal/wireguard"
)

// Keys is a tunnel's persistent WireGuard key pair. The private key never
// leaves the process except when persisted to the key store on disk.
type Keys struct {
	privateKey []byte
	publicKey  []byte
}

// keysFile is the on-disk representation of Keys (base64, WireGuard's
// standard encoding).
type keysFile struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

// GenerateKeys creates a fresh key pair.
func GenerateKeys() (*Keys, error) {
	priv, err := wireguard.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate private key: %w", err)
	}
	pub, err := wireguard.GetPublicKey(priv)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	return &Keys{privateKey: priv, publicKey: pub}, nil
}

// KeysFromPrivateKey builds a key pair from a base64 WireGuard private key,
// deriving the public half. It is for a host that keeps the private key in
// its own secret store (an OS keychain) rather than in a key store file.
func KeysFromPrivateKey(privateKey string) (*Keys, error) {
	priv, err := wireguard.DecodeKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	pub, err := wireguard.GetPublicKey(priv)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	return &Keys{privateKey: priv, publicKey: pub}, nil
}

// PublicKey returns the base64-encoded public key.
func (k *Keys) PublicKey() string { return wireguard.EncodeKey(k.publicKey) }

// PublicKeyBytes returns the raw public key bytes.
func (k *Keys) PublicKeyBytes() []byte { return k.publicKey }

// privateKeyBytes returns the raw private key bytes (unexported on purpose).
func (k *Keys) privateKeyBytes() []byte { return k.privateKey }

// Save writes the key pair to path (created with 0600 permissions). Parent
// directories are created as needed.
func (k *Keys) Save(path string) error {
	if path == "" {
		return fmt.Errorf("key store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create key store dir: %w", err)
	}
	kf := keysFile{
		PrivateKey: wireguard.EncodeKey(k.privateKey),
		PublicKey:  wireguard.EncodeKey(k.publicKey),
	}
	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write key store: %w", err)
	}
	return nil
}

// LoadKeys reads a key pair from path.
func LoadKeys(path string) (*Keys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kf keysFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("parse key store: %w", err)
	}
	priv, err := wireguard.DecodeKey(kf.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	pub, err := wireguard.GetPublicKey(priv)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	return &Keys{privateKey: priv, publicKey: pub}, nil
}

// LoadOrCreateKeys loads keys from path, or generates and saves a new pair if
// the file does not exist.
func LoadOrCreateKeys(path string) (*Keys, error) {
	k, err := LoadKeys(path)
	if err == nil {
		return k, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	k, err = GenerateKeys()
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err := k.Save(path); err != nil {
			return nil, err
		}
	}
	return k, nil
}
