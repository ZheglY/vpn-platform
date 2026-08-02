package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/curve25519"
)

type certificateGeneration struct {
	ca     *x509.Certificate
	server tls.Certificate
	client tls.Certificate
}

type drillReport struct {
	FormatVersion int               `json:"format_version"`
	Result        string            `json:"result"`
	Checks        map[string]string `json:"checks"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "secret rotation drill failed")
		os.Exit(1)
	}
}

func run() error {
	oldGeneration, err := newCertificateGeneration("old")
	if err != nil {
		return err
	}
	newGeneration, err := newCertificateGeneration("new")
	if err != nil {
		return err
	}
	bothRoots := x509.NewCertPool()
	bothRoots.AddCert(oldGeneration.ca)
	bothRoots.AddCert(newGeneration.ca)
	oldRoots := x509.NewCertPool()
	oldRoots.AddCert(oldGeneration.ca)
	newRoots := x509.NewCertPool()
	newRoots.AddCert(newGeneration.ca)

	if !handshake(oldGeneration.server, bothRoots, oldGeneration.client, bothRoots) ||
		!handshake(oldGeneration.server, bothRoots, newGeneration.client, bothRoots) {
		return fmt.Errorf("mTLS overlap phase failed")
	}
	if !handshake(newGeneration.server, bothRoots, oldGeneration.client, bothRoots) ||
		!handshake(newGeneration.server, bothRoots, newGeneration.client, bothRoots) {
		return fmt.Errorf("mTLS certificate cutover failed")
	}
	if handshake(newGeneration.server, newRoots, oldGeneration.client, bothRoots) ||
		handshake(oldGeneration.server, newRoots, newGeneration.client, newRoots) ||
		!handshake(newGeneration.server, newRoots, newGeneration.client, bothRoots) {
		return fmt.Errorf("mTLS retirement phase failed")
	}
	if !handshake(oldGeneration.server, oldRoots, oldGeneration.client, bothRoots) {
		return fmt.Errorf("mTLS rollback phase failed")
	}
	if err := verifyRealityGenerations(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(drillReport{
		FormatVersion: 1,
		Result:        "passed",
		Checks: map[string]string{
			"mtls":    "overlap_cutover_retirement_rollback",
			"reality": "distinct_x25519_generations",
		},
	})
}

func handshake(serverCertificate tls.Certificate, clientRoots *x509.CertPool, clientCertificate tls.Certificate, serverRoots *x509.CertPool) bool {
	serverConnection, clientConnection := net.Pipe()
	defer func() { _ = serverConnection.Close() }()
	defer func() { _ = clientConnection.Close() }()
	serverTLS := tls.Server(serverConnection, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientRoots,
	})
	clientTLS := tls.Client(clientConnection, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ServerName:   "rotation.invalid",
		Certificates: []tls.Certificate{clientCertificate},
		RootCAs:      serverRoots,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serverTLS.HandshakeContext(ctx)
	}()
	clientErr := clientTLS.HandshakeContext(ctx)
	serverErr := <-serverResult
	return clientErr == nil && serverErr == nil
}

func newCertificateGeneration(name string) (certificateGeneration, error) {
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return certificateGeneration{}, fmt.Errorf("generate rotation CA")
	}
	now := time.Now().UTC()
	caSerial, err := randomSerial()
	if err != nil {
		return certificateGeneration{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject:      pkix.Name{CommonName: "local-rotation-" + name},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		return certificateGeneration{}, fmt.Errorf("create rotation CA")
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return certificateGeneration{}, fmt.Errorf("parse rotation CA")
	}
	server, err := newLeaf(ca, caPrivate, "server-"+name, true)
	if err != nil {
		return certificateGeneration{}, err
	}
	client, err := newLeaf(ca, caPrivate, "client-"+name, false)
	if err != nil {
		return certificateGeneration{}, err
	}
	return certificateGeneration{ca: ca, server: server, client: client}, nil
}

func newLeaf(ca *x509.Certificate, caKey ed25519.PrivateKey, name string, server bool) (tls.Certificate, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate rotation leaf")
	}
	now := time.Now().UTC()
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if server {
		template.DNSNames = []string{"rotation.invalid"}
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create rotation leaf")
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: private}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial")
	}
	return serial, nil
}

func verifyRealityGenerations() error {
	firstPrivate := make([]byte, curve25519.ScalarSize)
	secondPrivate := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(firstPrivate); err != nil {
		return fmt.Errorf("generate first REALITY candidate")
	}
	if _, err := rand.Read(secondPrivate); err != nil {
		return fmt.Errorf("generate second REALITY candidate")
	}
	firstPublic, err := curve25519.X25519(firstPrivate, curve25519.Basepoint)
	if err != nil {
		return fmt.Errorf("derive first REALITY public key")
	}
	secondPublic, err := curve25519.X25519(secondPrivate, curve25519.Basepoint)
	if err != nil {
		return fmt.Errorf("derive second REALITY public key")
	}
	if string(firstPublic) == string(secondPublic) {
		return fmt.Errorf("REALITY key generations collided")
	}
	return nil
}
