package cryptoutil

import "testing"

func TestRandomBase64URL(t *testing.T) {
	got, err := RandomBase64URL(16)
	if err != nil {
		t.Fatalf("RandomBase64URL returned error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RandomBase64URL returned an empty string")
	}
	for _, r := range got {
		if r == '+' || r == '/' || r == '=' {
			t.Fatalf("RandomBase64URL returned non-url-safe character %q in %q", r, got)
		}
	}
}

func TestRandomBytesRejectsNonPositiveSize(t *testing.T) {
	if _, err := RandomBytes(0); err == nil {
		t.Fatal("expected error for zero byte count")
	}
}
