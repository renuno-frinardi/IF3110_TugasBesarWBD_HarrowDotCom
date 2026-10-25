package auth

// Import
import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Setup error msg
var (
	ErrInvalidHashFormat = errors.New("invalid password hash format")
	ErrEmptyPassword     = errors.New("password cannot be empty")
)

// Seteup const
const (
	pbkdf2Iterations = 100_000
	pbkdf2SaltLen    = 16
	pbkdf2KeyLen     = 32
)

// Hashing password dengan std lib milik Go
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}

	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$pbkdf2-sha256$i=%d$%s$%s", pbkdf2Iterations, b64Salt, b64Hash), nil
}

// Verifikasi password terhadap hash
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 5 || parts[1] != "pbkdf2-sha256" {
		return false, ErrInvalidHashFormat
	}

	var iterations int
	if _, err := fmt.Sscanf(parts[2], "i=%d", &iterations); err != nil || iterations < 1 {
		return false, ErrInvalidHashFormat
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false, ErrInvalidHashFormat
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(expectedHash) == 0 {
		return false, ErrInvalidHashFormat
	}

	computedHash, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(expectedHash))
	if err != nil {
		return false, ErrInvalidHashFormat
	}

	return subtle.ConstantTimeCompare(computedHash, expectedHash) == 1, nil
}
