package ssrf_test

import (
	"testing"

	"github.com/fixloop/fixloop/internal/ssrf"
)

func TestValidateURL_RejectsHTTP(t *testing.T) {
	if err := ssrf.ValidateURL("http://example.com"); err == nil {
		t.Fatal("expected error for http:// URL")
	}
}

func TestValidateURL_RejectsPrivateIP(t *testing.T) {
	// 127.0.0.1 is loopback — should be rejected
	if err := ssrf.ValidateURL("https://127.0.0.1"); err == nil {
		t.Fatal("expected error for loopback IP")
	}
}

func TestValidateURL_RejectsInternalHostname(t *testing.T) {
	// localhost resolves to 127.0.0.1
	if err := ssrf.ValidateURL("https://localhost"); err == nil {
		t.Fatal("expected error for localhost")
	}
}

func TestValidateURL_AcceptsPublicURL(t *testing.T) {
	// example.com is a real public domain
	if err := ssrf.ValidateURL("https://example.com"); err != nil {
		t.Fatalf("unexpected error for public URL: %v", err)
	}
}
