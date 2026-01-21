package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

const (
	// KeySize is the size of WireGuard keys in bytes.
	// Both private and public keys are 32 bytes for Curve25519.
	KeySize = 32

	// Curve25519 key clamping constants per RFC 7748.
	// These ensure the private key is a valid scalar for the curve.
	curve25519ClampLow  = 248 // Clear low 3 bits: key[0] &= 248
	curve25519ClampHigh = 127 // Clear high bit: key[31] &= 127
	curve25519SetBit    = 64  // Set bit 6: key[31] |= 64
)

// GeneratePrivateKey generates a new WireGuard private key.
func GeneratePrivateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}

	// Clamp the key as per Curve25519 requirements (RFC 7748)
	// This ensures the scalar is in the correct range for the curve
	key[0] &= curve25519ClampLow   // Clear low 3 bits
	key[31] &= curve25519ClampHigh // Clear high bit
	key[31] |= curve25519SetBit    // Set bit 6

	return key, nil
}

// GetPublicKey derives the public key from a private key.
func GetPublicKey(privateKey []byte) ([]byte, error) {
	if len(privateKey) != KeySize {
		return nil, fmt.Errorf("invalid private key size: expected %d bytes, got %d", KeySize, len(privateKey))
	}

	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	return publicKey, nil
}

// EncodeKey encodes a key to base64 (WireGuard's standard encoding).
func EncodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// DecodeKey decodes a base64-encoded key.
func DecodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode key: %w", err)
	}

	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key size: expected %d bytes, got %d", KeySize, len(key))
	}

	return key, nil
}

// ValidatePrivateKey checks if a private key is valid.
func ValidatePrivateKey(key []byte) error {
	if len(key) != KeySize {
		return fmt.Errorf("invalid key size: expected %d bytes, got %d", KeySize, len(key))
	}

	// Check if key is all zeros
	allZeros := true
	for _, b := range key {
		if b != 0 {
			allZeros = false
			break
		}
	}

	if allZeros {
		return errors.New("private key cannot be all zeros")
	}

	return nil
}

// ValidatePublicKey checks if a public key is valid.
func ValidatePublicKey(key []byte) error {
	if len(key) != KeySize {
		return fmt.Errorf("invalid key size: expected %d bytes, got %d", KeySize, len(key))
	}

	// Check if key is all zeros
	allZeros := true
	for _, b := range key {
		if b != 0 {
			allZeros = false
			break
		}
	}

	if allZeros {
		return errors.New("public key cannot be all zeros")
	}

	return nil
}
