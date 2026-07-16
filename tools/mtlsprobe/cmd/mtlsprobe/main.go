package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/httpclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 7 {
		return fmt.Errorf("usage: mtlsprobe METHOD URL CERT KEY CA WANT_STATUS")
	}
	method := os.Args[1]
	url := os.Args[2]
	certFile := os.Args[3]
	keyFile := os.Args[4]
	caFile := os.Args[5]
	wantStatus := os.Args[6]

	client, err := httpclient.NewMutualTLSClient(certFile, keyFile, []string{caFile}, 5*time.Second)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(`{}`))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	got := fmt.Sprintf("%d", resp.StatusCode)
	if got != wantStatus {
		return fmt.Errorf("status = %s, want %s", got, wantStatus)
	}
	return nil
}
