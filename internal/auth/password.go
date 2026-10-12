package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const Iterations = 600000

func PasswordValid(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) >= 12 && utf8.RuneCountInString(s) <= 128 && !strings.ContainsRune(s, 0)
}
func Derive(password string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, 32)
}
func Hash(password string) (hash, salt []byte, err error) {
	salt = make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return
	}
	hash, err = Derive(password, salt, Iterations)
	return
}
func Verify(password string, hash, salt []byte, iterations int) bool {
	v, e := Derive(password, salt, iterations)
	return e == nil && subtle.ConstantTimeCompare(v, hash) == 1
}
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func TokenHash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func Email(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || strings.Count(s, "@") != 1 || strings.ContainsAny(s, "\r\n\t ") {
		return "", fmt.Errorf("invalid email")
	}
	parts := strings.Split(s, "@")
	if len(parts[0]) == 0 || len(parts[0]) > 64 || strings.HasPrefix(parts[0], ".") || strings.HasSuffix(parts[0], ".") || strings.Contains(parts[0], "..") || !strings.Contains(parts[1], ".") {
		return "", fmt.Errorf("invalid email")
	}
	// Deliberately accept ASCII dot-atom addresses only, without quoted local
	// parts, display names or domain literals. Email is an account identifier.
	for _, r := range parts[0] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune(".!#$%&'*+-/=?^_`{|}~", r)) {
			return "", fmt.Errorf("invalid email")
		}
	}
	for _, label := range strings.Split(parts[1], ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid email")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", fmt.Errorf("invalid email")
			}
		}
	}
	return s, nil
}
