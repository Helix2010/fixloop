package util

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode"
)

// TitleHash returns the SHA-1 hex of the normalized title (lowercase, no punctuation/spaces).
// Used to deduplicate issues/backlog items by content rather than exact text.
func TitleHash(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
