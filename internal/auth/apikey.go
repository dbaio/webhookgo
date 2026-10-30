package auth

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strings"
)

// APIKeyPrefix starts every API key, so secret scanners can spot a leaked
// one.
const APIKeyPrefix = "wgo_"

// apiKeyDigits is how many base62 digits 32 random bytes take: 62^43 is just
// over 2^256.
const apiKeyDigits = 43

// NewAPIKey returns a new API key (the prefix, then 32 random bytes in
// base62, 47 characters in all) and the hash under which it is stored.
func NewAPIKey() (key string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source is broken
	}
	digits := new(big.Int).SetBytes(b).Text(62)
	key = APIKeyPrefix + strings.Repeat("0", apiKeyDigits-len(digits)) + digits
	return key, HashSecret(key)
}

// APIKey returns the key a request carries, in the Api-Key header or as a
// Bearer token, or "".
func APIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("Api-Key")); key != "" {
		return key
	}
	if scheme, key, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(key)
	}
	return ""
}
