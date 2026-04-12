package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	gossh "golang.org/x/crypto/ssh"
)

// SSHKeyPair holds an Ed25519 keypair in PEM/authorized_keys format.
type SSHKeyPair struct {
	PrivateKeyPEM []byte // OpenSSH PEM format, for git clone
	PublicKeyAuth []byte // authorized_keys format, for GitHub Deploy Key
}

// GenerateSSHKeyPair generates a new Ed25519 SSH keypair.
func GenerateSSHKeyPair() (*SSHKeyPair, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Marshal private key to OpenSSH PEM
	privPEM, err := gossh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEMBytes := pem.EncodeToMemory(privPEM)

	// Marshal public key to authorized_keys format
	sshPub, err := gossh.NewPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("create ssh public key: %w", err)
	}
	pubAuth := gossh.MarshalAuthorizedKey(sshPub)

	return &SSHKeyPair{
		PrivateKeyPEM: privPEMBytes,
		PublicKeyAuth: pubAuth,
	}, nil
}
