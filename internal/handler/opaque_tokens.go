package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// Each link kind gets a separate encryption key from its existing signing key.
func linkAEAD(key []byte, domain string) cipher.AEAD {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("opaque-link:" + domain))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func sealLink(key []byte, domain string, payload []byte) string {
	aead := linkAEAD(key, domain)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, payload, nil))
}

func openLink(key []byte, domain, token string) ([]byte, error) {
	aead := linkAEAD(key, domain)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid link")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
}
