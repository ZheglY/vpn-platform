package requestid

import "testing"

func TestValid(t *testing.T) {
	tests := map[string]bool{
		"abc":            false,
		"abcdefgh":       true,
		"abc_DEF-123:xy": true,
		"abc/def/ghi":    false,
		"abc def ghi":    false,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false,
	}
	for id, want := range tests {
		if got := Valid(id); got != want {
			t.Fatalf("Valid(%q) = %v, want %v", id, got, want)
		}
	}
}
