package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

const timeout = 3 * time.Second

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: httphealth http://host:port/path")
		os.Exit(2)
	}
	if err := check(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(ctx context.Context, rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		return fmt.Errorf("health endpoint must be a plain internal HTTP URL")
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("health request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned status %d", response.StatusCode)
	}
	return nil
}
