package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"

	"simplehost/core/web"
)

// The passcode screen shows and sets a passcode without it entering the
// chat: the screen makes a fresh P-256 key, the service answers with its
// own, and each passcode travels only sealed (AES-GCM) with the key both
// derive. The chat app sees the sealed bytes, never the passcode.

const screenInfo = "simple-host passcode screen v1"

var b64 = base64.RawURLEncoding

// passcodeStep is one of the screen's tools (tools.go): begin
// (passcode_screen_open: the site's state and the service's key), show, set
// (a sealed passcode) or generate.
func passcodeStep(action string) func(c *call) (string, string, any, error) {
	return func(c *call) (string, string, any, error) { return passcodeScreen(c, action) }
}

func passcodeScreen(c *call, action string) (string, string, any, error) {
	p, err := c.site("", nil)
	if err != nil {
		return "", "", nil, err
	}
	_, v, err := c.h.do(c.r, http.MethodGet, p, nil)
	if err != nil {
		return "", "", nil, err
	}
	site, _ := v.(map[string]any)
	if _, ok := site["access"]; !ok { // only the owner's view carries access
		return "", "", nil, web.Refuse(http.StatusForbidden, "not_yours", "Only the site's owner or their team can see or set its passcode.")
	}
	out := map[string]any{"site": site["name"], "owner": site["owner"], "passcode_protected": site["access"] == "passcode",
		"server_key": b64.EncodeToString(c.h.key.PublicKey().Bytes())}
	name := c.str("site")
	session := c.str("session")
	if action == "begin" {
		session = c.str("client_key")
	}
	if action == "begin" && session == "" { // the site's state only
		return "", "", out, nil
	}
	aead, err := c.h.screenCipher(session)
	if err != nil {
		return "", "", nil, err
	}
	code, _ := site["passcode"].(string)
	switch action {
	case "begin":
		out["session"] = session
		return "", "", out, nil
	case "show":
	case "set":
		nonce, err1 := b64.DecodeString(c.str("nonce"))
		sealed, err2 := b64.DecodeString(c.str("ciphertext"))
		var plain []byte
		if err1 == nil && err2 == nil && len(nonce) == aead.NonceSize() {
			plain, err = aead.Open(nil, nonce, sealed, []byte("set|"+name+"|"+session))
		}
		if err1 != nil || err2 != nil || plain == nil {
			return "", "", nil, web.Refuse(http.StatusBadRequest, "unreadable", "Could not open the sealed passcode. Close the screen and open it again.")
		}
		code = string(plain)
	case "generate":
		n, _ := rand.Int(rand.Reader, big.NewInt(100000000))
		code = fmt.Sprintf("%08d", n)
	default:
		return "", "", nil, web.Refuse(http.StatusBadRequest, "bad_action", "No such passcode screen step.")
	}
	if action != "show" {
		if _, _, err := c.h.do(c.r, http.MethodPatch, p, map[string]any{"passcode": code}); err != nil {
			return "", "", nil, err
		}
		out["passcode_protected"] = true
		if action == "set" {
			return "", "", out, nil
		}
	}
	if code == "" || out["passcode_protected"] != true {
		out["note"] = "No readable passcode. You can set a new one below."
		return "", "", out, nil
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	out["sealed"] = map[string]any{"nonce": b64.EncodeToString(nonce),
		"ciphertext": b64.EncodeToString(aead.Seal(nil, nonce, []byte(code), []byte("show|"+name+"|"+session)))}
	return "", "", out, nil
}

// screenCipher is the AES-GCM key shared with the screen whose public key
// (uncompressed P-256, base64url) is session.
func (h *server) screenCipher(session string) (cipher.AEAD, error) {
	bad := web.Refuse(http.StatusBadRequest, "bad_screen_key", "The passcode screen sent a key that does not work. Close it and open it again.")
	raw, err := b64.DecodeString(session)
	if err != nil {
		return nil, bad
	}
	public, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, bad
	}
	shared, err := h.key.ECDH(public)
	if err != nil {
		return nil, bad
	}
	key, err := hkdf.Key(sha256.New, shared, nil, screenInfo, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
