package wireguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePrivateKey(t *testing.T) {
	key, err := GeneratePrivateKey()
	require.NoError(t, err)
	assert.Len(t, key, KeySize)

	// Key should not be all zeros
	allZeros := true
	for _, b := range key {
		if b != 0 {
			allZeros = false
			break
		}
	}
	assert.False(t, allZeros, "key should not be all zeros")

	// Check clamping (first byte should have lower 3 bits cleared)
	assert.Equal(t, byte(0), key[0]&7)

	// Check clamping (last byte should have bit 7 clear and bit 6 set)
	assert.Equal(t, byte(0), key[31]&128)
	assert.Equal(t, byte(64), key[31]&64)
}

func TestGetPublicKey(t *testing.T) {
	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	publicKey, err := GetPublicKey(privateKey)
	require.NoError(t, err)
	assert.Len(t, publicKey, KeySize)

	// Public key should not be all zeros
	allZeros := true
	for _, b := range publicKey {
		if b != 0 {
			allZeros = false
			break
		}
	}
	assert.False(t, allZeros, "public key should not be all zeros")
}

func TestGetPublicKey_InvalidKeySize(t *testing.T) {
	_, err := GetPublicKey([]byte{1, 2, 3})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid private key size")
}

func TestEncodeDecodeKey(t *testing.T) {
	original, err := GeneratePrivateKey()
	require.NoError(t, err)

	// Encode
	encoded := EncodeKey(original)
	assert.NotEmpty(t, encoded)

	// Decode
	decoded, err := DecodeKey(encoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestDecodeKey_InvalidBase64(t *testing.T) {
	_, err := DecodeKey("not-valid-base64!@#$")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode key")
}

func TestDecodeKey_InvalidSize(t *testing.T) {
	// Encode a key that's too short
	shortKey := make([]byte, 16)
	encoded := EncodeKey(shortKey)

	_, err := DecodeKey(encoded)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid key size")
}

func TestValidatePrivateKey_Success(t *testing.T) {
	key, err := GeneratePrivateKey()
	require.NoError(t, err)

	err = ValidatePrivateKey(key)
	assert.NoError(t, err)
}

func TestValidatePrivateKey_Failure(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		wantErr string
	}{
		{
			name:    "wrong size",
			key:     make([]byte, 16),
			wantErr: "invalid key size",
		},
		{
			name:    "all zeros",
			key:     make([]byte, 32),
			wantErr: "cannot be all zeros",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrivateKey(tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidatePublicKey_Success(t *testing.T) {
	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	publicKey, err := GetPublicKey(privateKey)
	require.NoError(t, err)

	err = ValidatePublicKey(publicKey)
	assert.NoError(t, err)
}

func TestValidatePublicKey_Failure(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		wantErr string
	}{
		{
			name:    "wrong size",
			key:     make([]byte, 16),
			wantErr: "invalid key size",
		},
		{
			name:    "all zeros",
			key:     make([]byte, 32),
			wantErr: "cannot be all zeros",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePublicKey(tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestKeyRoundTrip(t *testing.T) {
	// Generate private key
	privateKey, err := GeneratePrivateKey()
	require.NoError(t, err)

	// Derive public key
	publicKey, err := GetPublicKey(privateKey)
	require.NoError(t, err)

	// Encode both
	privateKeyStr := EncodeKey(privateKey)
	publicKeyStr := EncodeKey(publicKey)

	// Decode both
	decodedPrivate, err := DecodeKey(privateKeyStr)
	require.NoError(t, err)
	decodedPublic, err := DecodeKey(publicKeyStr)
	require.NoError(t, err)

	// Verify they match
	assert.Equal(t, privateKey, decodedPrivate)
	assert.Equal(t, publicKey, decodedPublic)

	// Derive public key from decoded private key
	derivedPublic, err := GetPublicKey(decodedPrivate)
	require.NoError(t, err)
	assert.Equal(t, publicKey, derivedPublic)
}
