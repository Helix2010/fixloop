package crypto_test

import (
	"testing"

	"github.com/fixloop/fixloop/internal/crypto"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	keys := map[byte][]byte{1: key}

	plaintext := []byte("hello fixloop secret")
	ct, err := crypto.Encrypt(1, key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct[0] != 1 {
		t.Errorf("expected key_id=1, got %d", ct[0])
	}

	got, err := crypto.Decrypt(keys, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptUnknownKeyID(t *testing.T) {
	key := make([]byte, 32)
	keys := map[byte][]byte{1: key}
	ct, _ := crypto.Encrypt(1, key, []byte("data"))
	ct[0] = 99 // tamper key_id

	_, err := crypto.Decrypt(keys, ct)
	if err == nil {
		t.Fatal("expected error for unknown key_id")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	keys := map[byte][]byte{1: key}
	ct, _ := crypto.Encrypt(1, key, []byte("data"))
	ct[len(ct)-1] ^= 0xff // flip last byte

	_, err := crypto.Decrypt(keys, ct)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("same plaintext")
	ct1, _ := crypto.Encrypt(1, key, plaintext)
	ct2, _ := crypto.Encrypt(1, key, plaintext)
	if string(ct1) == string(ct2) {
		t.Error("two encryptions of same plaintext should differ (random nonce)")
	}
}
