package sites

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"simplehost/core/web"
)

// passcodeKeys are derived from PASSCODE_ENC_KEY: the cipher that seals
// passcodes and the unlock-cookie MAC key. Copied from the old
// host/access/passcode_keys.go so stored passcodes and remembered unlocks
// keep working.
type passcodeKeys struct {
	AEAD   cipher.AEAD // nil: PASSCODE_ENC_KEY unset
	MACKey []byte
}

func (k *passcodeKeys) set(b64 string) error {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
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
	return nil
}

// SealPasscode encrypts a passcode for the sites.passcode_enc column.
func (s *Sites) SealPasscode(siteID, code string) ([]byte, error) {
	if s.keys.AEAD == nil {
		return nil, errNoKey
	}
	nonce := make([]byte, s.keys.AEAD.NonceSize())
	rand.Read(nonce)
	return s.keys.AEAD.Seal(nonce, nonce, []byte(code), []byte("site:"+siteID)), nil
}

// OpenPasscode decrypts a site's passcode, so the owner can see it again.
func (s *Sites) OpenPasscode(site *Site) (string, error) {
	if s.keys.AEAD == nil {
		return "", errNoKey
	}
	n := s.keys.AEAD.NonceSize()
	if len(site.PasscodeEnc) < n+s.keys.AEAD.Overhead() {
		return "", errors.New("the stored passcode cannot be opened")
	}
	pt, err := s.keys.AEAD.Open(nil, site.PasscodeEnc[:n], site.PasscodeEnc[n:], []byte("site:"+site.ID))
	return string(pt), err
}

const unlockLife = 400 * 24 * time.Hour

// unlockCookie is the per-site cookie name. Over HTTPS only the __Host- form
// is read: a sibling host can plant a plain cookie, never a __Host- one.
func unlockCookie(r *http.Request, siteID string) string {
	sum := sha256.Sum256([]byte(siteID))
	name := "sh_pass_" + hex.EncodeToString(sum[:8])
	if web.HTTPS(r) {
		return "__Host-" + name
	}
	return name
}

func (s *Sites) unlockMAC(siteID, host string, exp int64, gen int) string {
	m := hmac.New(sha256.New, s.keys.MACKey)
	fmt.Fprintf(m, "v1|%s|%s|%d|%d", siteID, strings.ToLower(host), exp, gen)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SetUnlocked remembers in this browser, on this host, that the right
// passcode was given. It ends when the passcode or its generation changes.
func (s *Sites) SetUnlocked(w http.ResponseWriter, r *http.Request, site *Site) {
	exp := time.Now().Add(unlockLife).Unix()
	value := "v1." + strconv.FormatInt(exp, 10) + "." + strconv.Itoa(site.PasscodeGen) + "." + s.unlockMAC(site.ID, web.Host(r), exp, site.PasscodeGen)
	http.SetCookie(w, &http.Cookie{Name: unlockCookie(r, site.ID), Value: value, Path: "/", MaxAge: int(unlockLife.Seconds()),
		HttpOnly: true, Secure: web.HTTPS(r), SameSite: http.SameSiteLaxMode})
}

// Unlocked reports whether this browser gave the site's current passcode.
func (s *Sites) Unlocked(r *http.Request, site *Site) bool {
	if s.keys.MACKey == nil {
		return false
	}
	c, err := r.Cookie(unlockCookie(r, site.ID))
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return false
	}
	exp, err1 := strconv.ParseInt(parts[1], 10, 64)
	gen, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || gen != site.PasscodeGen || exp < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[3]), []byte(s.unlockMAC(site.ID, web.Host(r), exp, gen)))
}
