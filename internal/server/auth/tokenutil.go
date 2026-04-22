package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// BearerToken extracts the token from the Authorization header.
// Returns empty string if the header is missing or malformed.
func BearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

// SHA256Hex returns the lowercase hex-encoded SHA-256 hash of s.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
