package notify

import (
	"crypto/cipher"

	repo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/email"
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
var ErrNoKey = repo.ErrNoKey

// Ready reports whether new notifications can be created: their secrets
// are only ever stored sealed.
func (e Env) Ready() bool { return e.AEAD != nil }

// Secrets seals and opens a subscription's stored secrets.
func (e Env) Secrets() repo.Secrets { return repo.Secrets{AEAD: e.AEAD} }

// SealSecret seals one secret of subscription id ("" stays "").
func (e Env) SealSecret(id, plaintext string) (string, error) {
	return e.Secrets().Seal(id, plaintext)
}

// OpenSecret opens a secret SealSecret stored; a legacy plaintext value
// passes through unchanged.
func (e Env) OpenSecret(id, stored string) (string, error) {
	plain, err := e.Secrets().Open(id, stored)
	if err != nil {
		return "", Failure("secret_unreadable")
	}
	return plain, nil
}
