package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// NonceSize của AES-GCM chuẩn
const NonceSize = 12

var ErrOpenFailed = errors.New("cannot open sealed data")

// Seal mã hoá AES-256-GCM với nonce ngẫu nhiên; aad được xác thực nhưng không mã hoá
func Seal(key, plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

// Open giải mã; sai khoá, nonce, aad hay bản mã đều trả ErrOpenFailed (không lộ lý do)
func Open(key, ciphertext, nonce, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != NonceSize {
		return nil, ErrOpenFailed
	}
	pt, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrOpenFailed
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
