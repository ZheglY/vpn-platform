package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const KeyBytes = 32

type Keyring struct {
	activeVersion int
	keys          map[int][]byte
}

func NewKeyring(activeVersion int, keys map[int][]byte) (*Keyring, error) {
	if activeVersion <= 0 {
		return nil, fmt.Errorf("active key version must be positive")
	}
	copyKeys := make(map[int][]byte, len(keys))
	for version, key := range keys {
		if version <= 0 || len(key) != KeyBytes {
			return nil, fmt.Errorf("credential encryption key must be 32 bytes with a positive version")
		}
		copyKeys[version] = append([]byte(nil), key...)
	}
	if _, ok := copyKeys[activeVersion]; !ok {
		return nil, fmt.Errorf("active credential encryption key is missing")
	}
	return &Keyring{activeVersion: activeVersion, keys: copyKeys}, nil
}

func (k *Keyring) Encrypt(plaintext string, nonce []byte, binding string) ([]byte, int, error) {
	gcm, err := k.gcm(k.activeVersion)
	if err != nil {
		return nil, 0, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, 0, fmt.Errorf("invalid AES-GCM nonce length")
	}
	if binding == "" {
		return nil, 0, fmt.Errorf("credential binding is required")
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), []byte(binding))
	return append(append([]byte(nil), nonce...), sealed...), k.activeVersion, nil
}

func (k *Keyring) Decrypt(version int, envelope []byte, binding string) (string, error) {
	gcm, err := k.gcm(version)
	if err != nil {
		return "", err
	}
	if len(envelope) < gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("credential envelope is invalid")
	}
	if binding == "" {
		return "", fmt.Errorf("credential binding is required")
	}
	plaintext, err := gcm.Open(nil, envelope[:gcm.NonceSize()], envelope[gcm.NonceSize():], []byte(binding))
	if err != nil {
		return "", fmt.Errorf("decrypt credential envelope")
	}
	return string(plaintext), nil
}

func (k *Keyring) NonceSize() int {
	gcm, _ := k.gcm(k.activeVersion)
	return gcm.NonceSize()
}

func (k *Keyring) gcm(version int) (cipher.AEAD, error) {
	key, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("credential encryption key version is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return gcm, nil
}

type TokenHasher struct{ key []byte }

func NewTokenHasher(key []byte) (*TokenHasher, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("token HMAC key must be 32 bytes")
	}
	return &TokenHasher{key: append([]byte(nil), key...)}, nil
}

func (h *TokenHasher) Sum(token string) []byte {
	mac := hmac.New(sha256.New, h.key)
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil)
}

func DecodeKey(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode base64 key: %w", err)
	}
	if len(decoded) != KeyBytes {
		return nil, fmt.Errorf("decoded key must be 32 bytes")
	}
	return decoded, nil
}

func DecodeKeySet(value string) (map[int][]byte, error) {
	keys := make(map[int][]byte)
	for _, item := range strings.Split(value, ",") {
		versionText, encoded, ok := strings.Cut(strings.TrimSpace(item), ":")
		if !ok || versionText == "" || encoded == "" {
			return nil, fmt.Errorf("credential key set entries must use version:base64")
		}
		version, err := strconv.Atoi(versionText)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("credential key version must be positive")
		}
		if _, exists := keys[version]; exists {
			return nil, fmt.Errorf("credential key version is duplicated")
		}
		key, err := DecodeKey(encoded)
		if err != nil {
			return nil, err
		}
		keys[version] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one credential key is required")
	}
	return keys, nil
}
