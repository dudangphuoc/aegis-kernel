package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Chữ ký gói: ký trên danh sách "sha256  path" của mọi file (sắp theo path), giống sha256sum.
// Hub chỉ tin public key được ghim trong runtime, nên kho gói bị chiếm cũng không đẩy được gói lạ.

var ErrBadPackageSignature = errors.New("package signature invalid")

// PackageDigest dựng nội dung được ký từ map path -> nội dung file
func PackageDigest(files map[string][]byte) []byte {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(&buf, "%s  %s\n", hex.EncodeToString(sum[:]), p)
	}
	return buf.Bytes()
}

// SignPackage trả chữ ký base64 trên digest
func SignPackage(priv ed25519.PrivateKey, digest []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, digest))
}

// VerifyPackage chấp nhận nếu chữ ký khớp với một trong các public key tin cậy (hỗ trợ xoay khoá)
func VerifyPackage(trusted []ed25519.PublicKey, digest []byte, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadPackageSignature
	}
	for _, pub := range trusted {
		if ed25519.Verify(pub, digest, sig) {
			return nil
		}
	}
	return ErrBadPackageSignature
}

// ParsePublicKey đọc public key base64
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}
