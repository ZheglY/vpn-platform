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

func TestEncryptionKeyringSupportsOverlapAndRollback(t *testing.T) {
	oldKey := bytes.Repeat([]byte{5}, KeyBytes)
	newKey := bytes.Repeat([]byte{6}, KeyBytes)
	oldRing, err := NewKeyring(1, map[int][]byte{1: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	oldCiphertext, oldVersion, err := oldRing.Encrypt("old-secret", bytes.Repeat([]byte{1}, oldRing.NonceSize()), "credential")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewKeyring(2, map[int][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := rotated.Decrypt(oldVersion, oldCiphertext, "credential"); err != nil || plaintext != "old-secret" {
		t.Fatal("rotated keyring cannot decrypt old ciphertext")
	}
	newCiphertext, newVersion, err := rotated.Encrypt("new-secret", bytes.Repeat([]byte{2}, rotated.NonceSize()), "credential")
	if err != nil || newVersion != 2 {
		t.Fatal("rotated keyring did not encrypt with the active key")
	}
	rollback, err := NewKeyring(1, map[int][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := rollback.Decrypt(newVersion, newCiphertext, "credential"); err != nil || plaintext != "new-secret" {
		t.Fatal("rollback keyring cannot decrypt ciphertext created during rotation")
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

func TestTokenHasherKeyringSupportsOverlapAndRollback(t *testing.T) {
	oldKey := bytes.Repeat([]byte{3}, KeyBytes)
	newKey := bytes.Repeat([]byte{4}, KeyBytes)
	oldHasher, err := NewTokenHasherKeyring(1, map[int][]byte{1: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewTokenHasherKeyring(2, map[int][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := NewTokenHasherKeyring(1, map[int][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	token := "rotation-overlap-token"
	oldDigest := oldHasher.Sum(token)
	if rotated.ActiveVersion() != 2 || bytes.Equal(rotated.Sum(token), oldDigest) {
		t.Fatal("rotation did not move writes to the new HMAC key")
	}
	if !containsDigest(rotated.Candidates(token), oldDigest) {
		t.Fatal("overlap keyring cannot resolve an old token")
	}
	if !containsDigest(rollback.Candidates(token), rotated.Sum(token)) {
		t.Fatal("rollback keyring cannot resolve a token issued during rotation")
	}
}

func containsDigest(candidates [][]byte, want []byte) bool {
	for _, candidate := range candidates {
		if bytes.Equal(candidate, want) {
			return true
		}
	}
	return false
}

const sha256Size = 32

func TestDecodeKeySetSupportsRotationWindow(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, KeyBytes))
	keys, err := DecodeKeySet("1:" + encoded + ",2:" + encoded)
	if err != nil || len(keys) != 2 {
		t.Fatalf("decode key set: %v", err)
	}
}

func TestEncryptionKeyringRejectsEmptyAndUnboundedSets(t *testing.T) {
	key := bytes.Repeat([]byte{8}, KeyBytes)
	four := map[int][]byte{1: key, 2: key, 3: key, 4: key}
	if _, err := NewKeyring(4, four); err != nil {
		t.Fatalf("four-key encryption window was rejected: %v", err)
	}
	for name, keys := range map[string]map[int][]byte{
		"empty": {},
		"five":  {1: key, 2: key, 3: key, 4: key, 5: key},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewKeyring(1, keys); err == nil {
				t.Fatal("NewKeyring unexpectedly accepted an invalid key set")
			}
		})
	}
}

func TestDecodeKeySetRejectsEmptyAndUnboundedSets(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, KeyBytes))
	fourKeys := "1:" + encoded + ",2:" + encoded + ",3:" + encoded + ",4:" + encoded
	fiveKeys := "1:" + encoded + ",2:" + encoded + ",3:" + encoded + ",4:" + encoded + ",5:" + encoded
	if keys, err := DecodeKeySet(fourKeys); err != nil || len(keys) != MaxKeyringKeys {
		t.Fatalf("four-key configuration was rejected: keys=%d err=%v", len(keys), err)
	}
	for _, value := range []string{"", "   ", fiveKeys} {
		if _, err := DecodeKeySet(value); err == nil {
			t.Fatalf("DecodeKeySet(%q) unexpectedly succeeded", value)
		}
	}
}
