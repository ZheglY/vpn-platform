package httpserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func NewMutualTLSConfig(certFile, keyFile string, clientCAFiles []string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("server certificate and key files are required")
	}
	if len(clientCAFiles) == 0 {
		return nil, fmt.Errorf("at least one client CA file is required")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}

	clientCAs := x509.NewCertPool()
	for _, path := range clientCAFiles {
		pemBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read client CA %q: %w", path, err)
		}
		if !clientCAs.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("client CA %q does not contain a PEM certificate", path)
		}
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}, nil
}
