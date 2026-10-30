// Package auth holds the cryptography behind accounts: password hashing,
// the secrets of sessions and one-time links, and the session cookie.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters, the second recommended set of RFC 9106 (19 MiB, two
// passes, one lane): fine for a login, cheap enough for a small server.
const (
	argonMemory  = 19456 // KiB
	argonTime    = 2
	argonThreads = 1
	saltLen      = 16
	keyLen       = 32

	MinPassword = 10  // bytes
	MaxPassword = 256 // bytes
)

// hashes bounds how many argon2 derivations run at once: each takes 19 MiB,
// and the login endpoint is reachable by anyone.
var hashes = make(chan struct{}, 4)

// dummyHash is verified against when an email is unknown, so a login takes
// the same time whether the account exists or not.
const dummyHash = "$argon2id$v=19$m=19456,t=2,p=1$tv0qU78JUUFFc39g9NM2Ow$+ig/IHehpR3pClZ9uHy2PXcbdnYu6W1rFFv7+27AGmE"

// CheckPassword reports whether a password has an acceptable length.
func CheckPassword(password string) error {
	if len(password) < MinPassword || len(password) > MaxPassword {
		return fmt.Errorf("password must be between %d and %d characters", MinPassword, MaxPassword)
	}
	return nil
}

// HashPassword returns the argon2id hash of password in PHC string format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive(password, salt, argonTime, argonMemory, argonThreads)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC hash, comparing
// in constant time. A malformed hash is an error, never a match.
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("unsupported password hash")
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errors.New("malformed password hash")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("malformed password hash")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 || memory == 0 || time == 0 || threads == 0 {
		return false, errors.New("malformed password hash")
	}
	got := derive(password, salt, time, memory, threads)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy runs a verification that always fails, taking as long as a
// real one.
func VerifyDummy(password string) {
	_, _ = VerifyPassword(dummyHash, password)
}

func derive(password string, salt []byte, time, memory uint32, threads uint8) []byte {
	hashes <- struct{}{}
	defer func() { <-hashes }()
	return argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
}
