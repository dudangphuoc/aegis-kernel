package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/dudangphuoc/aegis-kernel/framework/core/wire/hubv1"
)

func busKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, BusKeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestHubTokenRoundTrip(t *testing.T) {
	in := HubToken{Host: "mq", Port: 5672, KeyID: "hub_ab12", BusKey: busKey(t), StoreID: "s-1", TenantCode: "acme", StoreCode: "q1"}
	s, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeHubToken("  " + s + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if out.KeyID != in.KeyID || !bytes.Equal(out.BusKey, in.BusKey) || out.StoreCode != "q1" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestHubTokenRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "phub2.abc", "phub1.!!!", "phub1." + base64.RawURLEncoding.EncodeToString([]byte(`{"host":"mq","keyId":"k","busKey":"AAAA","storeId":"s"}`))} {
		if _, err := DecodeHubToken(s); !errors.Is(err, ErrMalformedHubToken) {
			t.Fatalf("%q: expected ErrMalformedHubToken, got %v", s, err)
		}
	}
}

func TestDerivedKeysAreDistinctAndStable(t *testing.T) {
	k := busKey(t)
	d1, _ := DeviceKey(k, "hub_1")
	d2, _ := DeviceKey(k, "hub_1")
	d3, _ := DeviceKey(k, "hub_2")
	g, _ := GatewayKey(k, "hub_1")
	c, _ := ConsoleKey(k, "hub_1")
	if d1 != d2 || d1 == d3 {
		t.Fatal("device key must be deterministic per keyId")
	}
	if bytes.Equal(g, c) || bytes.Equal(g, k) {
		t.Fatal("gateway/console keys must differ from each other and from the bus key")
	}
}

func TestEnvelopeUpSignedAndTamperDetected(t *testing.T) {
	k, now := busKey(t), time.Now()
	env, err := SignUp(k, "m1", "hub_1", &hubv1.HubHeartbeat{HubVersion: "1.0.0"}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := VerifyUp(k, env, now)
	if err != nil {
		t.Fatal(err)
	}
	var hb hubv1.HubHeartbeat
	if err := payload.UnmarshalTo(&hb); err != nil || hb.HubVersion != "1.0.0" {
		t.Fatalf("payload mismatch: %v %v", err, &hb)
	}

	tampered := proto.Clone(env).(*hubv1.HubEnvelope)
	tampered.KeyId = "hub_2"
	if _, err := VerifyUp(k, tampered, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("changed keyId must fail, got %v", err)
	}
	if _, err := VerifyUp(busKey(t), env, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key must fail, got %v", err)
	}
	if _, err := VerifyUp(k, env, now.Add(MaxClockSkew+time.Minute)); !errors.Is(err, ErrStale) {
		t.Fatalf("old envelope must be stale, got %v", err)
	}
}

func TestEnvelopeDownSealed(t *testing.T) {
	k, now := busKey(t), time.Now()
	env, err := SealDown(k, "m2", "hub_1", &hubv1.HubRoleChanged{Role: "primary"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if env.Payload != nil || bytes.Contains(env.Sealed, []byte("primary")) {
		t.Fatal("down payload must not travel in clear")
	}
	payload, err := OpenDown(k, env, now)
	if err != nil {
		t.Fatal(err)
	}
	var rc hubv1.HubRoleChanged
	if err := payload.UnmarshalTo(&rc); err != nil || rc.Role != "primary" {
		t.Fatalf("payload mismatch: %v", err)
	}
	env.MessageId = "m3" // AAD đổi => không mở được
	if _, err := OpenDown(k, env, now); !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("changed id must fail, got %v", err)
	}
}

func TestSecretBoxBindsContext(t *testing.T) {
	box, err := NewSecretBox(base64.StdEncoding.EncodeToString(busKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := box.Seal([]byte("db-password"), "server:1")
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := box.Open(ct, nonce, "server:1"); err != nil || string(pt) != "db-password" {
		t.Fatalf("open failed: %v", err)
	}
	if _, err := box.Open(ct, nonce, "server:2"); !errors.Is(err, ErrOpenFailed) {
		t.Fatal("ciphertext copied to another row must not open")
	}
	if _, err := NewSecretBox("c2hvcnQ="); err == nil {
		t.Fatal("short KEK must be rejected")
	}
}

func TestPackageSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string][]byte{"platform.json": []byte(`{"key":"parties"}`), "bin/linux_amd64/parties": []byte("ELF")}
	sig := SignPackage(priv, PackageDigest(files))

	if err := VerifyPackage([]ed25519.PublicKey{other, pub}, PackageDigest(files), sig); err != nil {
		t.Fatalf("trusted key must verify: %v", err)
	}
	files["bin/linux_amd64/parties"] = []byte("EVIL")
	if err := VerifyPackage([]ed25519.PublicKey{pub}, PackageDigest(files), sig); !errors.Is(err, ErrBadPackageSignature) {
		t.Fatal("modified file must fail verification")
	}
	if err := VerifyPackage([]ed25519.PublicKey{other}, PackageDigest(map[string][]byte{"a": nil}), sig); err == nil {
		t.Fatal("untrusted key must fail")
	}
}
