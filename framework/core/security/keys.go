package security

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
)

// Mọi khoá dẫn xuất từ bus key bằng HKDF-SHA256 với salt/info riêng, để lộ một khoá không lộ khoá khác
// và gateway chỉ cần biết khoá dẫn xuất, không cần bus key.
const (
	saltDevice  = "platform-device"
	saltGateway = "platform-gateway"
	saltConsole = "platform-console"
)

// DeviceKey là giá trị header x-hub-key (thiết bị LAN và client internet qua gateway)
func DeviceKey(busKey []byte, keyID string) (string, error) {
	k, err := derive(busKey, saltDevice, "device-key:"+keyID, 32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(k), nil
}

// GatewayKey niêm phong request/response đi qua gateway
func GatewayKey(busKey []byte, keyID string) ([]byte, error) {
	return derive(busKey, saltGateway, "gateway:"+keyID, 32)
}

// ConsoleKey niêm phong kết quả truy vấn DB console của hub
func ConsoleKey(busKey []byte, keyID string) ([]byte, error) {
	return derive(busKey, saltConsole, "hub-query-result:"+keyID, 32)
}

func derive(secret []byte, salt, info string, size int) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, []byte(salt), info, size)
}
