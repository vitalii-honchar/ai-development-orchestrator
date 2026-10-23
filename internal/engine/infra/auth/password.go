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

const hashPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"

var (
	errUnsupportedPasswordHash = errors.New("unsupported password hash format")
	errInvalidPasswordHash     = errors.New("invalid password hash format")
	errInvalidPasswordSalt     = errors.New("invalid password hash salt")
	errInvalidPasswordKey      = errors.New("invalid password hash key")
)

func Hash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)

	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

func Verify(password, encodedHash string) (bool, error) {
	// Accept only our fixed parameters, avoiding unbounded work from malformed hashes.
	if !strings.HasPrefix(encodedHash, hashPrefix) {
		return false, errUnsupportedPasswordHash
	}
	parts := strings.Split(strings.TrimPrefix(encodedHash, hashPrefix), "$")
	if len(parts) != 2 || len(parts[0]) != 22 || len(parts[1]) != 43 {
		return false, errInvalidPasswordHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false, errInvalidPasswordSalt
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(hash) != 32 {
		return false, errInvalidPasswordKey
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)

	return subtle.ConstantTimeCompare(hash, actual) == 1, nil
}
