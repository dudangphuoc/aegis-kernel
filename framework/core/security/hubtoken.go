package security

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HubTokenPrefix đánh version định dạng token; đổi định dạng thì tăng số
const HubTokenPrefix = "phub1."

// BusKeySize là độ dài bus key (AES-256 / HMAC-SHA256)
const BusKeySize = 32

var ErrMalformedHubToken = errors.New("malformed hub token")

// HubToken là thứ operator dán vào hub khi ghép cặp: đủ để hub tự kết nối bus và chứng minh danh tính.
// Hiện một lần ở Control Plane; bus key không bao giờ rời khỏi cloud ở dạng khác.
type HubToken struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	VirtualHost string `json:"virtualHost"`
	UserName    string `json:"userName"`
	Password    string `json:"password"`
	Exchange    string `json:"exchange"`
	KeyID       string `json:"keyId"`
	BusKey      []byte `json:"busKey"`
	StoreID     string `json:"storeId"`
	TenantCode  string `json:"tenantCode"`
	StoreCode   string `json:"storeCode"`
}

// Encode trả "phub1." + base64url(JSON) không padding
func (t HubToken) Encode() (string, error) {
	if err := t.validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return HubTokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeHubToken đọc và kiểm token (bỏ khoảng trắng hai đầu do copy/paste)
func DecodeHubToken(s string) (HubToken, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, HubTokenPrefix) {
		return HubToken{}, fmt.Errorf("%w: missing %q prefix", ErrMalformedHubToken, HubTokenPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, HubTokenPrefix))
	if err != nil {
		return HubToken{}, fmt.Errorf("%w: %v", ErrMalformedHubToken, err)
	}
	var t HubToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return HubToken{}, fmt.Errorf("%w: %v", ErrMalformedHubToken, err)
	}
	if err := t.validate(); err != nil {
		return HubToken{}, err
	}
	return t, nil
}

func (t HubToken) validate() error {
	switch {
	case t.Host == "":
		return fmt.Errorf("%w: host is required", ErrMalformedHubToken)
	case t.KeyID == "":
		return fmt.Errorf("%w: keyId is required", ErrMalformedHubToken)
	case len(t.BusKey) != BusKeySize:
		return fmt.Errorf("%w: busKey must be %d bytes", ErrMalformedHubToken, BusKeySize)
	case t.StoreID == "":
		return fmt.Errorf("%w: storeId is required", ErrMalformedHubToken)
	}
	return nil
}
