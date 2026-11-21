package access

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// PasscodeKeys are the keys derived from PASSCODE_ENC_KEY: the cipher that
// seals passcodes and notification secrets, the unlock-cookie MAC key and the
// key of the connector's per-site visitor references. Access sets them once
// at startup (Set); Data reads the same value for notification secrets. The
// zero value holds none: passcodes are unavailable.
type PasscodeKeys struct {
	AEAD   cipher.AEAD // nil: PASSCODE_ENC_KEY unset
	MACKey []byte
	// RefKey derives the connector's per-site visitor references, so they
	// survive restarts. Nil: PASSCODE_ENC_KEY unset (see VisitorRef).
	RefKey []byte
}

// Set reads PASSCODE_ENC_KEY (32 bytes, standard base64). Empty leaves
// passcodes unavailable; anything else malformed is an error, so a typo stops
// startup instead of silently turning passcodes off.
func (k *PasscodeKeys) Set(b64 string) error {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		k.AEAD, k.MACKey, k.RefKey = nil, nil, nil
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != 32 {
		return errors.New("PASSCODE_ENC_KEY must be 32 random bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("simple-host passcode unlock cookie v1"))
	k.AEAD, k.MACKey = aead, mac.Sum(nil)
	ref := hmac.New(sha256.New, key)
	ref.Write([]byte("simple-host connector visitor reference v1"))
	k.RefKey = ref.Sum(nil)
	return nil
}

// VisitorRef keys the connector's per-site visitor references. It comes from
// PASSCODE_ENC_KEY so a reference in a notification still resolves after a
// restart; without that key it is fallback (the process-local export key).
func (k *PasscodeKeys) VisitorRef(fallback []byte) []byte {
	if k.RefKey != nil {
		return k.RefKey
	}
	return fallback
}
