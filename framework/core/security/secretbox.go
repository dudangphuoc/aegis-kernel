package security

import (
	"encoding/base64"
	"errors"
	"fmt"
)

// SecretBox mã hoá bí mật lưu trong DB (bus key, mật khẩu DB server) bằng KEK lấy từ môi trường.
// AAD là ngữ cảnh của bí mật (VD: "credential:<id>") để không thể chép bản mã sang dòng khác.
type SecretBox struct {
	kek []byte
}

// NewSecretBox nhận KEK dạng base64 (32 byte)
func NewSecretBox(kekBase64 string) (*SecretBox, error) {
	kek, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil {
		return nil, fmt.Errorf("decode KEK: %w", err)
	}
	if len(kek) != 32 {
		return nil, errors.New("KEK must be 32 bytes")
	}
	return &SecretBox{kek: kek}, nil
}

func (b *SecretBox) Seal(plaintext []byte, context string) (ciphertext, nonce []byte, err error) {
	return Seal(b.kek, plaintext, []byte(context))
}

func (b *SecretBox) Open(ciphertext, nonce []byte, context string) ([]byte, error) {
	return Open(b.kek, ciphertext, nonce, []byte(context))
}
