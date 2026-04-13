package shared

import (
	"encoding/json"

	"github.com/fixloop/fixloop/internal/playwright"
)

// ParseAuthConfig decodes a JSON map into a playwright.AuthConfig.
// Returns nil if raw is empty or malformed.
func ParseAuthConfig(raw []byte) *playwright.AuthConfig {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &playwright.AuthConfig{
		Type:     m["type"],
		Username: m["username"],
		Password: m["password"],
		Name:     m["name"],
		Value:    m["value"],
	}
}
