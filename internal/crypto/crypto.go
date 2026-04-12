package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext with AES-256-GCM.
// Output format: key_id(1 byte) | nonce(12 bytes) | ciphertext+tag
func Encrypt(keyID byte, key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)

	result := make([]byte, 1+len(nonce)+len(ct))
	result[0] = keyID
	copy(result[1:], nonce)
	copy(result[1+len(nonce):], ct)
	return result, nil
}

// Decrypt decrypts ciphertext using the key identified by the embedded key_id.
// keys maps key_id → 32-byte AES key (supports multiple keys for rotation).
func Decrypt(keys map[byte][]byte, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 13 { // 1 (key_id) + 12 (nonce minimum)
		return nil, fmt.Errorf("ciphertext too short")
	}
	keyID := ciphertext[0]
	key, ok := keys[keyID]
	if !ok {
		return nil, fmt.Errorf("unknown key_id: %d", keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < 1+nonceSize {
		return nil, fmt.Errorf("ciphertext too short for nonce")
	}
	nonce := ciphertext[1 : 1+nonceSize]
	ct := ciphertext[1+nonceSize:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}
