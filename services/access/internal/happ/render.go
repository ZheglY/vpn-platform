package happ

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/ZheglY/vpn-platform/services/access/internal/domain"
)

var shortIDPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{2}){0,8}$`)
var realityPublicKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func Render(vlessUUID string, endpoints []domain.EndpointSnapshot) (string, error) {
	if !uuidLike(vlessUUID) {
		return "", fmt.Errorf("VLESS UUID is invalid")
	}
	ordered := append([]domain.EndpointSnapshot(nil), endpoints...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Role != ordered[j].Role {
			return ordered[i].Role == "primary"
		}
		return ordered[i].NodeID < ordered[j].NodeID
	})
	lines := make([]string, 0, len(ordered))
	for _, endpoint := range ordered {
		line, err := renderEndpoint(vlessUUID, endpoint)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("at least one endpoint is required")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func renderEndpoint(vlessUUID string, endpoint domain.EndpointSnapshot) (string, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return "", err
	}

	query := url.Values{
		"encryption": {"none"},
		"flow":       {"xtls-rprx-vision"},
		"fp":         {"chrome"},
		"headerType": {"none"},
		"pbk":        {endpoint.RealityPublicKey},
		"security":   {"reality"},
		"sid":        {endpoint.ShortID},
		"sni":        {endpoint.ServerName},
		"type":       {"tcp"},
	}
	if endpoint.SpiderX != "" {
		query.Set("spx", endpoint.SpiderX)
	}
	u := &url.URL{
		Scheme:   "vless",
		User:     url.User(vlessUUID),
		Host:     net.JoinHostPort(endpoint.Address, fmt.Sprintf("%d", endpoint.Port)),
		RawQuery: query.Encode(),
		Fragment: endpoint.Label,
	}
	return u.String(), nil
}

func ValidateEndpoint(endpoint domain.EndpointSnapshot) error {
	if endpoint.Role != "primary" && endpoint.Role != "failover" {
		return fmt.Errorf("endpoint role is invalid")
	}
	if !validHost(endpoint.Address, true) || endpoint.Port < 1 || endpoint.Port > 65535 {
		return fmt.Errorf("endpoint address is invalid")
	}
	if !validHost(endpoint.ServerName, false) {
		return fmt.Errorf("REALITY server name is invalid")
	}
	if !realityPublicKeyPattern.MatchString(endpoint.RealityPublicKey) {
		return fmt.Errorf("REALITY public key is invalid")
	}
	if !shortIDPattern.MatchString(endpoint.ShortID) {
		return fmt.Errorf("REALITY short ID is invalid")
	}
	if endpoint.Label == "" || strings.ContainsAny(endpoint.Label, "\r\n") || len(endpoint.Label) > 64 {
		return fmt.Errorf("endpoint label is invalid")
	}
	if strings.ContainsAny(endpoint.SpiderX, "\r\n") || len(endpoint.SpiderX) > 255 {
		return fmt.Errorf("REALITY spider path is invalid")
	}
	return nil
}

func validHost(value string, allowIP bool) bool {
	if value == "" || len(value) > 253 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return false
	}
	if ip := net.ParseIP(value); ip != nil {
		return allowIP
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func uuidLike(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
