package cryptoutil

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func RandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("random byte count must be positive")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return b, nil
}

func RandomBase64URL(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
