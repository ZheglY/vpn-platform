package happ

import (
	"strings"
	"testing"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

func TestRenderVLESSRealityGolden(t *testing.T) {
	endpoints := []domain.EndpointSnapshot{
		{NodeID: "018f0e61-bca5-7a40-a06f-e4c0f53128af", Role: "failover", Address: "2001:db8::10", Port: 8443, ServerName: "fallback.example.com", RealityPublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", ShortID: "a1b2", Label: "VPN Failover"},
		{NodeID: "018f0e61-bca5-7a40-a06f-e4c0f53128ae", Role: "primary", Address: "vpn.example.com", Port: 443, ServerName: "cdn.example.com", RealityPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0011aabb", SpiderX: "/news?q=1", Label: "VPN Primary"},
	}
	got, err := Render("018f0e61-bca5-7a40-a06f-e4c0f53128ad", endpoints)
	if err != nil {
		t.Fatal(err)
	}
	want := "vless://018f0e61-bca5-7a40-a06f-e4c0f53128ad@vpn.example.com:443?encryption=none&flow=xtls-rprx-vision&fp=chrome&headerType=none&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&security=reality&sid=0011aabb&sni=cdn.example.com&spx=%2Fnews%3Fq%3D1&type=tcp#VPN%20Primary\n" +
		"vless://018f0e61-bca5-7a40-a06f-e4c0f53128ad@[2001:db8::10]:8443?encryption=none&flow=xtls-rprx-vision&fp=chrome&headerType=none&pbk=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB&security=reality&sid=a1b2&sni=fallback.example.com&type=tcp#VPN%20Failover\n"
	if got != want {
		t.Fatalf("golden mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestRenderRejectsHeaderInjection(t *testing.T) {
	_, err := Render("018f0e61-bca5-7a40-a06f-e4c0f53128ad", []domain.EndpointSnapshot{{Role: "primary", Address: "vpn.example.com", Port: 443, ServerName: "ok.example", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "bad\r\nheader"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateEndpointRejectsMalformedPublicFields(t *testing.T) {
	t.Parallel()
	base := domain.EndpointSnapshot{Role: "primary", Address: "vpn.example.com", Port: 443, ServerName: "cdn.example.com", RealityPublicKey: strings.Repeat("A", 43), ShortID: "0011", Label: "Primary"}
	tests := []struct {
		name   string
		mutate func(*domain.EndpointSnapshot)
	}{
		{name: "address path", mutate: func(endpoint *domain.EndpointSnapshot) { endpoint.Address = "vpn.example.com/path" }},
		{name: "IP server name", mutate: func(endpoint *domain.EndpointSnapshot) { endpoint.ServerName = "192.0.2.1" }},
		{name: "padded public key", mutate: func(endpoint *domain.EndpointSnapshot) { endpoint.RealityPublicKey = strings.Repeat("A", 42) + "=" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := base
			test.mutate(&endpoint)
			if err := ValidateEndpoint(endpoint); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
