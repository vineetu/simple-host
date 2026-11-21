package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

func testEnv(t *testing.T, fill byte) Env {
	t.Helper()
	block, err := aes.NewCipher(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return Env{AEAD: aead}
}

func assertSealed(t *testing.T, stored, plain string) {
	t.Helper()
	if !strings.HasPrefix(stored, "enc1:") || strings.Contains(stored, plain) {
		t.Fatal("notification value was not sealed")
	}
}

func TestSecretSealOpen(t *testing.T) {
	env := testEnv(t, 1)
	sealed, err := env.SealSecret("sub_a", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	assertSealed(t, sealed, testSecret)
	if plain, err := env.OpenSecret("sub_a", sealed); err != nil || plain != testSecret {
		t.Fatal("round trip failed")
	}
	again, err := env.SealSecret("sub_a", testSecret)
	if err != nil || sealed == again {
		t.Fatal("nonce was reused")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "enc1:"))
	if err != nil || len(raw) != 12+len(testSecret)+16 {
		t.Fatal("unexpected stored format")
	}
	raw[len(raw)-1] ^= 1
	for _, tc := range []struct {
		name, id, value string
		env             Env
	}{
		{"wrong AD", "sub_b", sealed, env},
		{"tampered", "sub_a", "enc1:" + base64.StdEncoding.EncodeToString(raw), env},
		{"wrong key", "sub_a", sealed, testEnv(t, 7)},
		{"missing key", "sub_a", sealed, Env{}},
		{"bad base64", "sub_a", "enc1:!", env},
		{"short ciphertext", "sub_a", "enc1:AAAA", env},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.env.OpenSecret(tc.id, tc.value); !errors.Is(err, Failure("secret_unreadable")) {
				t.Fatal("unreadable value did not fail cleanly")
			}
		})
	}
	for _, e := range []Env{env, {}} {
		for _, plain := range []string{"", testSecret, "Bearer legacy"} {
			if got, err := e.OpenSecret("sub_a", plain); err != nil || got != plain {
				t.Fatal("legacy value changed")
			}
		}
		if got, err := e.SealSecret("sub_a", ""); err != nil || got != "" {
			t.Fatal("empty value changed")
		}
	}
	if _, err := (Env{}).SealSecret("sub_a", testSecret); !errors.Is(err, ErrNoKey) {
		t.Fatal("sealing without a key accepted")
	}
	// A custom header beginning enc1: is still plaintext input and is sealed once.
	value, err := env.SealSecret("sub_a:header", "enc1:literal-header")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := env.OpenSecret("sub_a:header", value); err != nil || got != "enc1:literal-header" {
		t.Fatal("header prefix changed")
	}
}

func TestSealedDelivery(t *testing.T) {
	env := testEnv(t, 1)
	x := newReceiver(t)
	c := trusting(x.server)
	second, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	n := model.Notification{ID: "sub_sealed", URL: x.server.URL, Source: "owner", PrevSecretUntil: &until, AuthHeaderName: "Authorization"}
	n.Secret, _ = env.SealSecret(n.ID, second)
	n.PrevSecret, _ = env.SealSecret(n.ID, testSecret)
	n.AuthHeaderValue, _ = env.SealSecret(n.ID+":header", "Bearer receiver")
	if r := c.send(context.Background(), env, n, "evt_sealed", []byte(`{}`)); !r.Delivered {
		t.Fatal("sealed delivery failed")
	}
	got := x.headers[0]
	if len(strings.Fields(got.Get("webhook-signature"))) != 2 || got.Get("Authorization") != "Bearer receiver" {
		t.Fatal("rotation or custom header failed")
	}
	for _, field := range []string{"secret", "prev_secret", "auth_header_value"} {
		t.Run(field, func(t *testing.T) {
			broken := n
			switch field {
			case "secret":
				broken.Secret = "enc1:!"
			case "prev_secret":
				broken.PrevSecret = "enc1:!"
			case "auth_header_value":
				broken.AuthHeaderValue = "enc1:!"
			}
			if r := c.send(context.Background(), env, broken, "evt_bad", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
				t.Fatal("unreadable field was sent")
			}
		})
	}
	if r := c.send(context.Background(), Env{}, n, "evt_missing", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
		t.Fatal("sealed delivery without key accepted")
	}
	if len(x.headers) != 1 {
		t.Fatal("unreadable delivery reached receiver")
	}
	// A keyless server still delivers legacy rows, including the auth header.
	n.Secret, n.PrevSecret, n.AuthHeaderValue = second, testSecret, "Bearer receiver"
	if r := c.send(context.Background(), Env{}, n, "evt_legacy", []byte(`{}`)); !r.Delivered {
		t.Fatal("legacy delivery without key failed")
	}
}

func TestVAPIDSealIsBoundToItsPurpose(t *testing.T) {
	env := testEnv(t, 1)
	sealed, err := env.seal("vapid", "private")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := env.open("vapid", sealed); err != nil || got != "private" {
		t.Fatal("vapid round trip")
	}
	if _, err := env.OpenSecret("vapid", sealed); err == nil {
		t.Fatal("vapid key opened as a notification secret")
	}
}
