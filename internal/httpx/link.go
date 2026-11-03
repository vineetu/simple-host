package httpx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// linkAEAD derives a link kind's own AEAD cipher from its signing key: each
// domain gets a separate encryption key from the same underlying key.
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

// SealLink encrypts and authenticates payload into an opaque, URL-safe
// token scoped to domain: only OpenLink with the same key and domain can
// read it back. Used for export, preview and storage-file links on the Host
// side and archive-claim links on the Hack side, each with its own domain
// string so a token minted for one kind is rejected by another.
func SealLink(key []byte, domain string, payload []byte) string {
	aead := linkAEAD(key, domain)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, payload, nil))
}

// OpenLink reverses SealLink, rejecting a token minted with a different
// key or domain, or one that has been tampered with.
func OpenLink(key []byte, domain, token string) ([]byte, error) {
	aead := linkAEAD(key, domain)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid link")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
}
