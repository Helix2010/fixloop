package ssrf

import (
	"fmt"
	"net"
	"net/url"
)

// ValidateURL checks that rawURL is an HTTPS URL pointing to a public IP.
// Call once on save, and again at agent runtime (防 DNS rebinding).
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed (got %q)", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no hostname")
	}
	return checkHostname(u.Hostname())
}

// ValidateHostname re-resolves a hostname and checks it's not private.
// Call this immediately before starting Playwright to defend against DNS rebinding.
func ValidateHostname(hostname string) error {
	return checkHostname(hostname)
}

func checkHostname(hostname string) error {
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %q: %w", hostname, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivate(ip) {
			return fmt.Errorf("hostname %q resolves to private IP %s", hostname, addr)
		}
	}
	return nil
}

func isPrivate(ip net.IP) bool {
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range private {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
