package contract

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

type battleCryptoVectors struct {
	X25519 struct {
		AlicePrivate string `json:"alice_private_hex"`
		AlicePublic  string `json:"alice_public_hex"`
		BobPrivate   string `json:"bob_private_hex"`
		BobPublic    string `json:"bob_public_hex"`
		Shared       string `json:"shared_secret_hex"`
	} `json:"x25519"`
	HKDF struct {
		IKM         string `json:"ikm_hex"`
		Salt        string `json:"salt_hex"`
		Info        string `json:"info_hex"`
		OutputBytes int    `json:"output_bytes"`
		PRK         string `json:"prk_hex"`
		OKM         string `json:"okm_hex"`
	} `json:"hkdf_sha256"`
	HMAC struct {
		Key  string `json:"key_hex"`
		Data string `json:"data_hex"`
		Tag  string `json:"tag_hex"`
	} `json:"hmac_sha256"`
	AEAD struct {
		Key        string `json:"key_hex"`
		Nonce      string `json:"nonce_hex"`
		AAD        string `json:"aad_hex"`
		Plaintext  string `json:"plaintext_hex"`
		Ciphertext string `json:"ciphertext_hex"`
		Tag        string `json:"tag_hex"`
	} `json:"chacha20_poly1305"`
}

func TestBattleCryptoVectorsMatchGoIndependentImplementations(t *testing.T) {
	vectors := loadBattleCryptoVectors(t)
	alicePrivate := decodeCryptoHex(t, vectors.X25519.AlicePrivate)
	alicePublic := decodeCryptoHex(t, vectors.X25519.AlicePublic)
	bobPrivate := decodeCryptoHex(t, vectors.X25519.BobPrivate)
	bobPublic := decodeCryptoHex(t, vectors.X25519.BobPublic)
	expectedShared := decodeCryptoHex(t, vectors.X25519.Shared)
	if actual, err := curve25519.X25519(alicePrivate, curve25519.Basepoint); err != nil ||
		!hmac.Equal(actual, alicePublic) {
		t.Fatalf("RFC 7748 Alice public parity failed: %v", err)
	}
	aliceShared, err := curve25519.X25519(alicePrivate, bobPublic)
	if err != nil || !hmac.Equal(aliceShared, expectedShared) {
		t.Fatalf("RFC 7748 Alice shared parity failed: %v", err)
	}
	bobShared, err := curve25519.X25519(bobPrivate, alicePublic)
	if err != nil || !hmac.Equal(bobShared, expectedShared) {
		t.Fatalf("RFC 7748 Bob shared parity failed: %v", err)
	}

	ikm := decodeCryptoHex(t, vectors.HKDF.IKM)
	salt := decodeCryptoHex(t, vectors.HKDF.Salt)
	info := decodeCryptoHex(t, vectors.HKDF.Info)
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(ikm)
	if !hmac.Equal(extract.Sum(nil), decodeCryptoHex(t, vectors.HKDF.PRK)) {
		t.Fatal("RFC 5869 PRK parity failed")
	}
	okm := make([]byte, vectors.HKDF.OutputBytes)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, info), okm); err != nil ||
		!hmac.Equal(okm, decodeCryptoHex(t, vectors.HKDF.OKM)) {
		t.Fatalf("RFC 5869 OKM parity failed: %v", err)
	}

	mac := hmac.New(sha256.New, decodeCryptoHex(t, vectors.HMAC.Key))
	_, _ = mac.Write(decodeCryptoHex(t, vectors.HMAC.Data))
	if !hmac.Equal(mac.Sum(nil), decodeCryptoHex(t, vectors.HMAC.Tag)) {
		t.Fatal("RFC 4231 HMAC-SHA-256 parity failed")
	}

	aead, err := chacha20poly1305.New(decodeCryptoHex(t, vectors.AEAD.Key))
	if err != nil {
		t.Fatal(err)
	}
	sealed := aead.Seal(nil,
		decodeCryptoHex(t, vectors.AEAD.Nonce),
		decodeCryptoHex(t, vectors.AEAD.Plaintext),
		decodeCryptoHex(t, vectors.AEAD.AAD))
	expectedSealed := append(
		decodeCryptoHex(t, vectors.AEAD.Ciphertext),
		decodeCryptoHex(t, vectors.AEAD.Tag)...)
	if !hmac.Equal(sealed, expectedSealed) {
		t.Fatal("RFC 8439 ChaCha20-Poly1305 parity failed")
	}
}

func loadBattleCryptoVectors(t *testing.T) battleCryptoVectors {
	t.Helper()
	path := filepath.Join("..", "..", "..", "shared", "contracts", "fixtures", "battle", "wire", "crypto-vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vectors battleCryptoVectors
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}

func decodeCryptoHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) == 0 || hex.EncodeToString(decoded) != value {
		t.Fatalf("invalid crypto vector hex %q: %v", value, err)
	}
	return decoded
}
