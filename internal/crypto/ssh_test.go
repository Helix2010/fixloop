package crypto_test

import (
	"strings"
	"testing"

	"github.com/fixloop/fixloop/internal/crypto"
)

func TestGenerateSSHKeyPair(t *testing.T) {
	kp, err := crypto.GenerateSSHKeyPair()
	if err != nil {
		t.Fatalf("GenerateSSHKeyPair: %v", err)
	}
	if !strings.Contains(string(kp.PrivateKeyPEM), "OPENSSH PRIVATE KEY") {
		t.Errorf("private key not in OpenSSH PEM format: %s", kp.PrivateKeyPEM[:50])
	}
	if !strings.HasPrefix(string(kp.PublicKeyAuth), "ssh-ed25519 ") {
		t.Errorf("public key not in authorized_keys format: %s", kp.PublicKeyAuth[:30])
	}
}

func TestSSHKeyPairUniqueness(t *testing.T) {
	kp1, _ := crypto.GenerateSSHKeyPair()
	kp2, _ := crypto.GenerateSSHKeyPair()
	if string(kp1.PublicKeyAuth) == string(kp2.PublicKeyAuth) {
		t.Error("two generated keypairs should be different")
	}
}
