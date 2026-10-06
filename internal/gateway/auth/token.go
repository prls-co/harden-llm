// Package auth validates the single bearer credential accepted by the proxy.
package auth

import (
	"crypto/subtle"
	"strings"
)

func ValidToken(token string) bool {
	if len(token) < 32 || len(token) > 512 || strings.TrimSpace(token) != token {
		return false
	}
	for _, char := range token {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func MatchBearer(values []string, expected string) bool {
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.Count(values[0], " ") != 1 {
		return false
	}
	provided := strings.TrimPrefix(values[0], "Bearer ")
	return provided != "" && strings.TrimSpace(provided) == provided && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
