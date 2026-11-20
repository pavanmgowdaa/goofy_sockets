package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"time"
)

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func HashPassword(p string) (string, error) {
	if len(p) < 12 {
		return "", errors.New("password must be at least 12 characters")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), e
}
func CheckPassword(hash, p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}
func Token(secret, userID, role string, ttl time.Duration) (string, error) {
	c := Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: userID, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}
func Parse(secret, s string) (*Claims, error) {
	t, e := jwt.ParseWithClaims(s, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return []byte(secret), nil
	})
	if e != nil {
		return nil, e
	}
	c, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}
func RandomToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}

// SHA256Hex is for high-entropy bearer secrets (refresh/QR/device tokens), not
// user passwords. Passwords must continue to use the adaptive bcrypt functions.
func SHA256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func HMACSHA256Hex(secret string, value []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))
}
