//go:build linux

package main

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotifyReady(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	t.Setenv("NOTIFY_SOCKET", path)
	if err := notifyReady(); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	n, _, err := listener.ReadFromUnix(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if message := string(buffer[:n]); !strings.HasPrefix(message, "READY=1\n") {
		t.Fatalf("notification = %q, want READY=1", message)
	}
}
