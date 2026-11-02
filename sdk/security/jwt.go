// Package security cung cấp ký và xác thực JWT (HS256) dùng chung giữa Kernel Gateway và các Module.
package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// EnvJWTSecret là biến môi trường chứa khóa ký HS256. Kernel đảm bảo biến này tồn tại
// trước khi spawn module (tiến trình con kế thừa env).
const EnvJWTSecret = "AEGIS_JWT_SECRET"

const (
	SecurityLevelStandard = "STANDARD"
	SecurityLevelMFA      = "MFA_HIGH_ASSURANCE"
	minSecretLen          = 32
)

var (
	ErrMalformedToken   = errors.New("malformed token")
	ErrInvalidSignature = errors.New("invalid token signature")
	ErrTokenExpired     = errors.New("token expired")
	ErrMissingSecret    = fmt.Errorf("%s is not set", EnvJWTSecret)
)

// TokenClaims là payload JWT chuẩn của Aegis
type TokenClaims struct {
	TokenID       string `json:"jti"`
	Subject       string `json:"sub"`       // User ID (VD: USR-ADMIN-01)
	Username      string `json:"usr"`       // Username
	TenantID      string `json:"tid"`       // Tenant ID (bắt buộc khớp với X-Tenant-ID)
	Role          string `json:"rol"`       // TENANT_ADMIN, USER, ...
	SecurityLevel string `json:"sec_level"` // STANDARD | MFA_HIGH_ASSURANCE
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// SecretFromEnv đọc khóa ký từ AEGIS_JWT_SECRET
func SecretFromEnv() ([]byte, error) {
	s := os.Getenv(EnvJWTSecret)
	if s == "" {
		return nil, ErrMissingSecret
	}
	if len(s) < minSecretLen {
		return nil, fmt.Errorf("%s must be at least %d characters", EnvJWTSecret, minSecretLen)
	}
	return []byte(s), nil
}

// NewTokenID sinh jti ngẫu nhiên
func NewTokenID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateSecret sinh khóa ngẫu nhiên 256-bit (hex) cho môi trường dev khi chưa cấu hình secret
func GenerateSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SignToken ký claims thành chuỗi JWT HS256
func SignToken(secret []byte, claims TokenClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signingInput + "." + sign(secret, signingInput), nil
}

// VerifyToken kiểm tra chữ ký, thuật toán và hạn dùng của JWT
func VerifyToken(secret []byte, token string, now time.Time) (*TokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformedToken
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Alg != "HS256" {
		return nil, ErrMalformedToken
	}

	expected := sign(secret, parts[0]+"."+parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, ErrInvalidSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformedToken
	}
	var claims TokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrMalformedToken
	}
	if claims.ExpiresAt == 0 || now.Unix() >= claims.ExpiresAt {
		return nil, ErrTokenExpired
	}
	return &claims, nil
}

func sign(secret []byte, input string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
