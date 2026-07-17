package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

func NewMutualTLSClient(certFile, keyFile string, caFiles []string, timeout time.Duration) (*http.Client, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("client certificate and key files are required")
	}
	if len(caFiles) == 0 {
		return nil, fmt.Errorf("at least one server CA file is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}

	rootCAs, err := loadCertPool(caFiles)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS13,
				Certificates: []tls.Certificate{cert},
				RootCAs:      rootCAs,
			},
		},
	}, nil
}

func NewTLSClient(caFiles []string, timeout time.Duration) (*http.Client, error) {
	if len(caFiles) == 0 {
		return nil, fmt.Errorf("at least one server CA file is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	rootCAs, err := loadCertPool(caFiles)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootCAs,
		}},
	}, nil
}

func loadCertPool(caFiles []string) (*x509.CertPool, error) {
	rootCAs := x509.NewCertPool()
	for _, path := range caFiles {
		pemBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read server CA %q: %w", path, err)
		}
		if !rootCAs.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("server CA %q does not contain a PEM certificate", path)
		}
	}
	return rootCAs, nil
}
