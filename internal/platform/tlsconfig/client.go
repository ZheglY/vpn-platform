package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// NewClient builds a TLS 1.3 client configuration from environment-staged
// credential files. Client certificates are optional, but cert and key must be
// supplied together.
func NewClient(caFiles []string, certFile, keyFile string) (*tls.Config, error) {
	if len(caFiles) == 0 {
		return nil, fmt.Errorf("at least one CA file is required")
	}
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("client certificate and key must be supplied together")
	}

	roots := x509.NewCertPool()
	for _, path := range caFiles {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file does not contain a PEM certificate")
		}
	}

	result := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	if certFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}
