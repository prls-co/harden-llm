// Package auth implements bootstrap local identity and opaque bearer sessions.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime            uint32 = 3
	argonMemory          uint32 = 64 * 1024
	argonThreads         uint8  = 4
	argonSaltBytes              = 16
	argonHashBytes       uint32 = 32
	minimumPasswordBytes        = 12
	maximumPasswordBytes        = 1024
)

func hashPassword(password string, randomSource io.Reader) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := io.ReadFull(randomSource, salt); err != nil {
		return "", errors.New("auth: password salt generation failed")
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonHashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPassword(encoded, password string) bool {
	if validatePassword(password) != nil {
		burnPassword(password)
		return false
	}
	salt, expected, ok := parsePasswordHash(encoded)
	if !ok {
		burnPassword(password)
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonHashBytes)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func parsePasswordHash(encoded string) ([]byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=4" {
		return nil, nil, false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltBytes {
		return nil, nil, false
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) != int(argonHashBytes) {
		return nil, nil, false
	}
	return salt, hash, true
}

func burnPassword(password string) {
	if len(password) > maximumPasswordBytes {
		password = password[:maximumPasswordBytes]
	}
	_ = argon2.IDKey([]byte(password), []byte("harden-llm-dummy"), argonTime, argonMemory, argonThreads, argonHashBytes)
}

func validatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes {
		return errors.New("auth: password must contain 12 to 1024 UTF-8 bytes")
	}
	return nil
}
