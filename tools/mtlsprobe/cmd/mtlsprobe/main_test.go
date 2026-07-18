package main

import "testing"

func TestParseArgs(t *testing.T) {
	t.Parallel()
	base := []string{"POST", "https://example.invalid", "client.crt", "client.key", "ca.crt", "200"}
	opts, err := parseArgs(append(base, "Idempotency-Key", "smoke-key", "print-body"))
	if err != nil {
		t.Fatal(err)
	}
	if opts.headerName != "Idempotency-Key" || opts.headerValue != "smoke-key" || !opts.printBody {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestParseArgsRejectsHeaderInjectionAndUnknownMode(t *testing.T) {
	t.Parallel()
	base := []string{"POST", "https://example.invalid", "client.crt", "client.key", "ca.crt", "200"}
	for _, args := range [][]string{
		append(base, "Idempotency-Key\r\nInjected", "value"),
		append(base, "Idempotency-Key", "value", "verbose"),
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%q) succeeded", args)
		}
	}
}
