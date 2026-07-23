package httpdecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Strict decodes one bounded JSON value and rejects contract drift.
func Strict(body io.Reader, maxBytes int64, output any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if int64(len(payload)) > maxBytes {
		return fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("response contains trailing JSON")
	}
	return nil
}
