package credential

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestKeyringEncryptDecryptAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeyBytes)
	keyring, err := NewKeyring(7, map[int][]byte{7: key})
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x11}, keyring.NonceSize())
	ciphertext, version, err := keyring.Encrypt("018f0e61-bca5-7a40-a06f-e4c0f53128ad", nonce, "credential-id")
	if err != nil {
		t.Fatal(err)
	}
	if version != 7 || bytes.Contains(ciphertext, []byte("018f0e61")) {
		t.Fatal("credential was not envelope encrypted")
	}
	plaintext, err := keyring.Decrypt(version, ciphertext, "credential-id")
	if err != nil || plaintext != "018f0e61-bca5-7a40-a06f-e4c0f53128ad" {
		t.Fatalf("decrypt: %q, %v", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := keyring.Decrypt(version, ciphertext, "credential-id"); err == nil {
		t.Fatal("tampered envelope was accepted")
	}
	if _, err := keyring.Decrypt(version, append([]byte(nil), ciphertext...), "other-credential"); err == nil {
		t.Fatal("credential envelope was accepted under another row binding")
	}
}

func TestTokenHasherIsKeyedAndDeterministic(t *testing.T) {
	first, _ := NewTokenHasher(bytes.Repeat([]byte{1}, KeyBytes))
	second, _ := NewTokenHasher(bytes.Repeat([]byte{2}, KeyBytes))
	a := first.Sum("token")
	if !bytes.Equal(a, first.Sum("token")) || bytes.Equal(a, second.Sum("token")) || len(a) != sha256Size {
		t.Fatal("unexpected HMAC behavior")
	}
}

const sha256Size = 32

func TestDecodeKeySetSupportsRotationWindow(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, KeyBytes))
	keys, err := DecodeKeySet("1:" + encoded + ",2:" + encoded)
	if err != nil || len(keys) != 2 {
		t.Fatalf("decode key set: %v", err)
	}
}
