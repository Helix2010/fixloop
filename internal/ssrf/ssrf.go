package ssrf

import (
	"fmt"
	"net"
	"net/url"
	"strings"
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

// ExtractHostname strips the scheme and port from a raw URL string,
// returning just the hostname. Faster than url.Parse for hot paths.
func ExtractHostname(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}

// privateNets is parsed once at startup for efficient repeated SSRF checks.
var privateNets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"0.0.0.0/8",          // "This" network
		"10.0.0.0/8",         // RFC1918
		"100.64.0.0/10",      // CGNAT (RFC6598)
		"127.0.0.0/8",        // Loopback
		"169.254.0.0/16",     // Link-local
		"172.16.0.0/12",      // RFC1918
		"192.168.0.0/16",     // RFC1918
		"224.0.0.0/4",        // Multicast
		"240.0.0.0/4",        // Reserved
		"255.255.255.255/32", // Broadcast
		"::1/128",            // IPv6 loopback
		"fc00::/7",           // IPv6 unique local
		"fe80::/10",          // IPv6 link-local
		"ff00::/8",           // IPv6 multicast
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			privateNets = append(privateNets, network)
		}
	}
}

func isPrivate(ip net.IP) bool {
	// Unwrap IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1) before checking.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range privateNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
