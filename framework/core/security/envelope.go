package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dudangphuoc/aegis-kernel/framework/core/wire/hubv1"
)

// MaxClockSkew: envelope lệch giờ quá mức này bị từ chối (chống phát lại message cũ)
const MaxClockSkew = 10 * time.Minute

var (
	ErrBadSignature = errors.New("envelope signature mismatch")
	ErrStale        = errors.New("envelope outside allowed clock skew")
)

// SignUp đóng message chiều lên (hub -> cloud): payload rõ, ký HMAC-SHA256 bằng bus key.
// Dữ liệu ký gồm id, keyId, giây gửi, type URL và payload để không thể ghép payload sang envelope khác.
func SignUp(busKey []byte, messageID, keyID string, msg proto.Message, now time.Time) (*hubv1.HubEnvelope, error) {
	payload, err := anypb.New(msg)
	if err != nil {
		return nil, err
	}
	env := &hubv1.HubEnvelope{MessageId: messageID, KeyId: keyID, Payload: payload, SentAt: timestamppb.New(now)}
	env.Signature = upMAC(busKey, env)
	return env, nil
}

// VerifyUp kiểm chữ ký và độ lệch giờ, trả payload
func VerifyUp(busKey []byte, env *hubv1.HubEnvelope, now time.Time) (*anypb.Any, error) {
	if env.GetPayload() == nil || env.GetSentAt() == nil {
		return nil, ErrBadSignature
	}
	if !hmac.Equal(env.GetSignature(), upMAC(busKey, env)) {
		return nil, ErrBadSignature
	}
	if err := checkSkew(env.GetSentAt().AsTime(), now); err != nil {
		return nil, err
	}
	return env.GetPayload(), nil
}

// SealDown đóng message chiều xuống (cloud -> hub): Any được mã hoá AES-GCM, AAD = id\nkeyId\nsentAt
func SealDown(busKey []byte, messageID, keyID string, msg proto.Message, now time.Time) (*hubv1.HubEnvelope, error) {
	payload, err := anypb.New(msg)
	if err != nil {
		return nil, err
	}
	plain, err := proto.Marshal(payload)
	if err != nil {
		return nil, err
	}
	env := &hubv1.HubEnvelope{MessageId: messageID, KeyId: keyID, SentAt: timestamppb.New(now)}
	env.Sealed, env.Nonce, err = Seal(busKey, plain, downAAD(env))
	if err != nil {
		return nil, err
	}
	return env, nil
}

// OpenDown giải mã và kiểm độ lệch giờ, trả payload
func OpenDown(busKey []byte, env *hubv1.HubEnvelope, now time.Time) (*anypb.Any, error) {
	if env.GetSentAt() == nil {
		return nil, ErrOpenFailed
	}
	plain, err := Open(busKey, env.GetSealed(), env.GetNonce(), downAAD(env))
	if err != nil {
		return nil, err
	}
	if err := checkSkew(env.GetSentAt().AsTime(), now); err != nil {
		return nil, err
	}
	var payload anypb.Any
	if err := proto.Unmarshal(plain, &payload); err != nil {
		return nil, ErrOpenFailed
	}
	return &payload, nil
}

func upMAC(key []byte, env *hubv1.HubEnvelope) []byte {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%s\n%s\n%d\n%s\n", env.GetMessageId(), env.GetKeyId(), env.GetSentAt().GetSeconds(), env.GetPayload().GetTypeUrl())
	m.Write(env.GetPayload().GetValue())
	return m.Sum(nil)
}

func downAAD(env *hubv1.HubEnvelope) []byte {
	return fmt.Appendf(nil, "%s\n%s\n%d", env.GetMessageId(), env.GetKeyId(), env.GetSentAt().GetSeconds())
}

func checkSkew(sent, now time.Time) error {
	d := now.Sub(sent)
	if d > MaxClockSkew || d < -MaxClockSkew {
		return ErrStale
	}
	return nil
}
