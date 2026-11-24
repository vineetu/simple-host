package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"simplehost/app"
)

// signer is the service's VAPID key: browser push services accept a push
// only when it is signed with the key the browser subscribed with.
type signer struct {
	priv *ecdsa.PrivateKey
	pub  string // base64url, uncompressed point: what browsers subscribe with
}

var b64 = base64.RawURLEncoding

// vapid reads SH_VAPID_KEY (base64url of the 32-byte private key). Empty
// means push is off.
func vapid(raw string) (*signer, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := b64.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SH_VAPID_KEY is not base64url: %w", err)
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), b)
	if err != nil {
		return nil, fmt.Errorf("SH_VAPID_KEY: %w", err)
	}
	pub, err := priv.PublicKey.Bytes()
	return &signer{priv, b64.EncodeToString(pub)}, err
}

// PublicKey is what a browser subscribes with ("" when push is off).
func PublicKey(a *app.App) string {
	k, err := vapid(a.Config.VAPIDKey)
	if err != nil || k == nil {
		return ""
	}
	return k.pub
}

// send encrypts payload for one browser (RFC 8291, aes128gcm) and posts it
// to its push service, signed (RFC 8292). It returns the push service's status.
func (k *signer) send(ctx context.Context, client *http.Client, endpoint, p256dh, auth string, payload []byte) (int, error) {
	body, err := encrypt(p256dh, auth, payload)
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	jwt, err := k.token(u.Scheme + "://" + u.Host)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+k.pub)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	if res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("push service answered %d", res.StatusCode)
	}
	return res.StatusCode, nil
}

// token is the signed VAPID claim for one push service.
func (k *signer) token(audience string) (string, error) {
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims := b64.EncodeToString([]byte(`{"aud":"` + audience + `","exp":` + strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10) + `,"sub":"mailto:hello@simple-host.app"}`))
	sum := sha256.Sum256([]byte(head + "." + claims))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return head + "." + claims + "." + b64.EncodeToString(sig), nil
}

// encrypt is RFC 8291: one record, keyed from a fresh ECDH key and the
// browser's p256dh and auth secret.
func encrypt(p256dh, auth string, plain []byte) ([]byte, error) {
	uaRaw, err := b64.DecodeString(p256dh)
	if err != nil {
		return nil, err
	}
	secret, err := b64.DecodeString(auth)
	if err != nil {
		return nil, err
	}
	ua, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, err
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	// hkdf fails only on lengths, and these are fixed.
	prk, _ := hkdf.Extract(sha256.New, shared, secret)
	ikm, _ := hkdf.Expand(sha256.New, prk, "WebPush: info\x00"+string(uaRaw)+string(asPub), 32)
	salt := make([]byte, 16)
	rand.Read(salt)
	prk, _ = hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	head := make([]byte, 0, 86)
	head = append(head, salt...)
	head = binary.BigEndian.AppendUint32(head, 4096)
	head = append(head, byte(len(asPub)))
	head = append(head, asPub...)
	return gcm.Seal(head, nonce, append(plain, 2), nil), nil // 2: the last record
}
