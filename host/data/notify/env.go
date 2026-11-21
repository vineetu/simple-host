package notify

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/model"
)

// Env is what a notification reads from the install at the moment of use:
// PASSCODE_ENC_KEY's cipher (nil: unset), the key behind the connector's
// visitor references, and the notice mailer (nil: none). The caller builds
// it per call because the key is installed after the engine exists.
type Env struct {
	AEAD       cipher.AEAD
	VisitorRef []byte
	Mailer     email.NoticeSender
}

// ErrNoKey is a seal without PASSCODE_ENC_KEY.
var ErrNoKey = errors.New("PASSCODE_ENC_KEY is not set")

// Ready reports whether new notifications can be created: their secrets
// are only ever stored sealed.
func (e Env) Ready() bool { return e.AEAD != nil }

// Secrets seals and opens a subscription's stored secrets.
func (e Env) Secrets() model.NotificationSecrets {
	return model.NotificationSecrets{Seal: e.SealSecret, Open: e.OpenSecret}
}

// SealSecret seals one secret of subscription id ("" stays "").
func (e Env) SealSecret(id, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return e.seal("notification:"+id, plaintext)
}

// OpenSecret opens a secret SealSecret stored; a legacy plaintext value
// passes through unchanged.
func (e Env) OpenSecret(id, stored string) (string, error) {
	if !strings.HasPrefix(stored, "enc1:") {
		return stored, nil
	}
	plain, err := e.open("notification:"+id, stored)
	if err != nil {
		return "", Failure("secret_unreadable")
	}
	return plain, nil
}

// seal is "enc1:" + base64(nonce | AES-GCM ciphertext) bound to ad.
func (e Env) seal(ad, plaintext string) (string, error) {
	if e.AEAD == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, e.AEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "enc1:" + base64.StdEncoding.EncodeToString(e.AEAD.Seal(nonce, nonce, []byte(plaintext), []byte(ad))), nil
}

var errSealed = errors.New("sealed value unreadable")

// open reverses seal; every failure is errSealed (ErrNoKey without a key).
func (e Env) open(ad, stored string) (string, error) {
	encoded, ok := strings.CutPrefix(stored, "enc1:")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil {
		return "", errSealed
	}
	if e.AEAD == nil {
		return "", ErrNoKey
	}
	n := e.AEAD.NonceSize()
	if len(raw) < n+e.AEAD.Overhead() {
		return "", errSealed
	}
	plain, err := e.AEAD.Open(nil, raw[:n], raw[n:], []byte(ad))
	if err != nil {
		return "", errSealed
	}
	return string(plain), nil
}
