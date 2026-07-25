package nodeagent

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestClientPinsExactNodeSPIFFEIdentity(t *testing.T) {
	client, err := NewClient(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	expected := "spiffe://vpn-service/ns/local/sa/node-agent-primary"
	transport := client.clientForNode(expected).Transport
	if unwrapper, ok := transport.(interface{ Unwrap() http.RoundTripper }); ok {
		transport = unwrapper.Unwrap()
	}
	verify := transport.(*http.Transport).TLSClientConfig.VerifyConnection
	if err := verify(verifiedNodeState(expected)); err != nil {
		t.Fatalf("matching identity rejected: %v", err)
	}
	if err := verify(verifiedNodeState("spiffe://vpn-service/ns/local/sa/node-agent-failover")); err == nil {
		t.Fatal("wrong node identity accepted")
	}
	if err := verify(tls.ConnectionState{}); err == nil {
		t.Fatal("unverified node certificate accepted")
	}
}

func TestNewClientRequiresTLSBaseTransport(t *testing.T) {
	_, err := NewClient(&http.Client{}, time.Second)
	if err == nil {
		t.Fatal("client without TLS transport was accepted")
	}
}

func verifiedNodeState(identity string) tls.ConnectionState {
	uri, _ := url.Parse(identity)
	certificate := &x509.Certificate{URIs: []*url.URL{uri}}
	return tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}}
}
