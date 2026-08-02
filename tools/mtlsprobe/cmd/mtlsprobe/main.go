package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/httpclient"
)

type options struct {
	method      string
	url         string
	certFile    string
	keyFile     string
	caFile      string
	wantStatus  string
	headerName  string
	headerValue string
	bodyFile    string
	printBody   bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}

	client, err := httpclient.NewMutualTLSClient(opts.certFile, opts.keyFile, []string{opts.caFile}, 5*time.Second)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := []byte(`{}`)
	if opts.bodyFile != "" {
		body, err = os.ReadFile(opts.bodyFile)
		if err != nil {
			return fmt.Errorf("read request body file: %w", err)
		}
		if len(body) > 64<<10 {
			return fmt.Errorf("request body file is too large")
		}
	}
	req, err := http.NewRequestWithContext(ctx, opts.method, opts.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.headerName != "" {
		req.Header.Set(opts.headerName, opts.headerValue)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	got := fmt.Sprintf("%d", resp.StatusCode)
	if got != opts.wantStatus {
		return fmt.Errorf("status = %s, want %s", got, opts.wantStatus)
	}
	if opts.printBody {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		if _, err := os.Stdout.Write(body); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	return nil
}

func parseArgs(args []string) (options, error) {
	if len(args) != 6 && len(args) != 8 && len(args) != 9 {
		return options{}, fmt.Errorf("usage: mtlsprobe METHOD URL CERT KEY CA WANT_STATUS [HEADER VALUE|body-file PATH [print-body]]")
	}
	opts := options{
		method: args[0], url: args[1], certFile: args[2], keyFile: args[3], caFile: args[4], wantStatus: args[5],
	}
	if len(args) >= 8 {
		if args[6] == "body-file" {
			if strings.TrimSpace(args[7]) == "" {
				return options{}, fmt.Errorf("invalid body file")
			}
			opts.bodyFile = args[7]
		} else {
			if strings.TrimSpace(args[6]) == "" || strings.ContainsAny(args[6], "\r\n") || strings.ContainsAny(args[7], "\r\n") {
				return options{}, fmt.Errorf("invalid HTTP header")
			}
			opts.headerName, opts.headerValue = args[6], args[7]
		}
	}
	if len(args) == 9 {
		if args[8] != "print-body" {
			return options{}, fmt.Errorf("unknown output mode")
		}
		opts.printBody = true
	}
	return opts, nil
}
