package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewSecret returns a random secret for a session or a one-time link (32
// bytes, base64url without padding) and the hash under which it is stored.
func NewSecret() (secret string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source is broken
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, HashSecret(secret)
}

// HashSecret is the SHA-256 of a secret: what the database keeps.
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
