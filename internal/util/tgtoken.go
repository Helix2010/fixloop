package util

import (
	"crypto/sha256"
	"encoding/hex"
)

// TGBindKey derives the system_config key for a TG bind token from raw token bytes.
// Tokens are stored as SHA-256 hashes so that database exposure cannot be replayed.
func TGBindKey(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "tg_bind_" + hex.EncodeToString(sum[:])
}
