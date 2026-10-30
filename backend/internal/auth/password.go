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

const (
	argonMemory = 19 * 1024
	argonTime   = 2
	argonLanes  = 1
	argonKeyLen = 32
	argonSalt   = 16
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return HashWithSalt(password, salt), nil
}

func HashWithSalt(password string, salt []byte) string {
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonLanes, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonLanes,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("parola özeti biçimi geçersiz")
	}
	var memory, iterations uint32
	var lanes uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &lanes); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, lanes, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
