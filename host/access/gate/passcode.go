package gate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
)

// Site passcodes.
//
// An owner may put one passcode on a whole site. Every address of the site
// then answers an "Enter the passcode" page (401, noindex, nothing of the
// site in it; a request that is not a page still answers the plain line
// "This site is protected") until the visitor enters the passcode; after that an
// unlock cookie for that one host lets them in until the passcode changes or
// the owner signs everyone out. The saved-data API of a protected site takes
// that same cookie, or the owner's (or the admin's) key; previews bypass it
// (their token is the capability).
//
// Storage: the passcode is sealed with AES-256-GCM under PASSCODE_ENC_KEY
// (sites.passcode_enc; the site id is the associated data), so the owner can
// read it back. It is checked by opening the sealed value and comparing in
// constant time. A slow hash on top would protect nothing: whoever holds the
// key can open the sealed value anyway, and without the key neither the
// sealed value nor a hash is of use (a hash of a 6-digit code falls to a
// dictionary in seconds). sites.view_password_hash (the removed July
// feature) is never read.
//
// Unlock cookie: __Host-sh_pass_<16 hex of sha256(site id)> (a plain
// sh_pass_... over HTTP), host-only, HttpOnly, SameSite=Lax, Max-Age 400
// days (the browsers' cap). Its value is v1.<expiry>.<generation>.<mac>, the
// MAC an HMAC-SHA256 over the site id, the host, the expiry and the
// generation under a key derived from PASSCODE_ENC_KEY. The generation
// (sites.passcode_generation) goes up on every change and on "sign everyone
// out", which ends every unlock at once.
//
// Where it is enforced: serveSiteFile (every Go path to a site's files) and
// the `passcode` marker file next to `current`, which makes nginx and Caddy
// hand disk-served addresses (custom domains, the content host) to
// /internal/passcode instead of serving the file. The marker is written
// before the database on lock and removed after it on unlock, so a failure
// in between leaves the site locked, never open. Take-down (410) and offline
// (503) win over the passcode (401).

// passcodeCookieMaxAge is the unlock's life: 400 days, the longest Max-Age
// browsers keep. An unlock never ends on its own before that; a new
// passcode or "sign everyone out" ends it.
const passcodeCookieMaxAge = 400 * 24 * time.Hour

// passcodeMaxLength is the longest passcode (in characters).
const passcodeMaxLength = 128

// passcodeState is the server key and the brute-force counters.
type passcodeState struct {
	// limits is the handler's own snapshot (h.limits), copied in once at
	// construction (NewSiteHandlers) since the rate-limit methods below have
	// no Handler of their own. Zero in a bare notifyClient{secrets: &passcodeState{}} test literal, which
	// never calls wrongTry/sweep.
	Limits edition.Limits
	// keys are the PASSCODE_ENC_KEY keys (SetPasscodeKey).
	keys accounts.PasscodeKeys

	mu        sync.Mutex
	ipTries   *httpx.RateLimiter   // site id + "|" + client address
	siteTries *httpx.RateLimiter   // site id
	locked    map[string]time.Time // "ip:" or "site:" + key: refused until
	Now       func() time.Time
	// ipSalt makes the logged address hash unlinkable across restarts.
	ipSalt []byte
}

// SetPasscodeKey reads PASSCODE_ENC_KEY (accounts.PasscodeKeys.Set).
func (h *Handler) SetPasscodeKey(b64 string) error { return h.Passcode.keys.Set(b64) }

// PasscodeKeys is the passcode key set Data reads for notification secrets.
func (h *Handler) PasscodeKeys() *accounts.PasscodeKeys { return &h.Passcode.keys }

// VisitorRefKey keys the connector's per-site visitor references
// (accounts.PasscodeKeys.VisitorRef).
func (h *Handler) VisitorRefKey() []byte { return h.Passcode.keys.VisitorRef(h.ExportKey) }

// PasscodesReady reports whether a passcode can be set on this server:
// SITE_PASSCODES is on, the key is set and sites have addresses of their own.
func (h *Handler) PasscodesReady() bool {
	return h.Limits.SitePasscodes && h.Passcode.keys.AEAD != nil && !h.Hosts.Addressing().SharedOrigin()
}

func (p *passcodeState) clock() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// seal encrypts a passcode for one site.
func (p *passcodeState) Seal(siteID, code string) ([]byte, error) {
	return p.sealWithAD("site:"+siteID, code)
}

func (p *passcodeState) sealWithAD(ad, code string) ([]byte, error) {
	if p.keys.AEAD == nil {
		return nil, errPasscodeNoKey
	}
	nonce := make([]byte, p.keys.AEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.keys.AEAD.Seal(nonce, nonce, []byte(code), []byte(ad)), nil
}

// open decrypts a site's sealed passcode.
func (p *passcodeState) open(siteID string, enc []byte) (string, error) {
	return p.openWithAD("site:"+siteID, enc)
}

func (p *passcodeState) openWithAD(ad string, enc []byte) (string, error) {
	if p.keys.AEAD == nil {
		return "", errPasscodeNoKey
	}
	n := p.keys.AEAD.NonceSize()
	if len(enc) < n+p.keys.AEAD.Overhead() {
		return "", errPasscodeSealed
	}
	pt, err := p.keys.AEAD.Open(nil, enc[:n], enc[n:], []byte(ad))
	if err != nil {
		return "", errPasscodeSealed
	}
	return string(pt), nil
}

var (
	errPasscodeNoKey  = errors.New("PASSCODE_ENC_KEY is not set")
	errPasscodeSealed = errors.New("the stored passcode cannot be opened with PASSCODE_ENC_KEY")
)

// passcodeMatches compares a try with the real passcode in constant time
// (over digests, so the length is not measured either).
func passcodeMatches(try, real string) bool {
	a := sha256.Sum256([]byte(try))
	b := sha256.Sum256([]byte(real))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// normalizePasscode trims surrounding white space and checks the length.
// There are no other rules: any characters, digits only included. Control characters are refused because the gate's
// one-line box could never send them.
func normalizePasscode(code string, min int) (string, error) {
	code = strings.TrimSpace(code)
	if !utf8.ValidString(code) {
		return "", errors.New("the passcode is not valid text")
	}
	n := utf8.RuneCountInString(code)
	if n < min {
		return "", fmt.Errorf("a passcode needs at least %d characters", min)
	}
	if n > passcodeMaxLength {
		return "", fmt.Errorf("a passcode can be at most %d characters", passcodeMaxLength)
	}
	for _, r := range code {
		if unicode.IsControl(r) {
			return "", errors.New("a passcode cannot hold line breaks, tabs or other control characters")
		}
	}
	return code, nil
}

// generatePasscode makes a random passcode of PASSCODE_MIN_LENGTH digits (at
// least 6).
func generatePasscode(n int) (string, error) {
	if n < 6 {
		n = 6
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String(), nil
}

// --- The unlock cookie ---

// passcodeCookieName is the per-site cookie name. Over HTTPS only the
// __Host- form is ever read: a sibling host can plant a Domain cookie of a
// plain name, never a __Host- one.
func passcodeCookieName(r *http.Request, siteID string) string {
	sum := sha256.Sum256([]byte(siteID))
	name := "sh_pass_" + hex.EncodeToString(sum[:8])
	if httpx.IsHTTPS(r) {
		return "__Host-" + name
	}
	return name
}

func (p *passcodeState) cookieMAC(siteID, host string, exp int64, gen int) string {
	m := hmac.New(sha256.New, p.keys.MACKey)
	fmt.Fprintf(m, "v1|%s|%s|%d|%d", siteID, strings.ToLower(host), exp, gen)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setUnlockCookie lets this browser into the site on this host.
func (h *Handler) setUnlockCookie(w http.ResponseWriter, r *http.Request, siteID string, gen int) {
	exp := h.Passcode.clock().Add(passcodeCookieMaxAge).Unix()
	value := "v1." + strconv.FormatInt(exp, 10) + "." + strconv.Itoa(gen) + "." + h.Passcode.cookieMAC(siteID, httpx.HostName(r), exp, gen)
	http.SetCookie(w, &http.Cookie{
		Name:     passcodeCookieName(r, siteID),
		Value:    value,
		Path:     "/",
		MaxAge:   int(passcodeCookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   httpx.IsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// unlocked reports whether the request carries a valid unlock cookie for
// this site, on this host, for the passcode's current generation.
func (h *Handler) unlocked(r *http.Request, siteID string, gen int) bool {
	if h.Passcode.keys.MACKey == nil {
		return false
	}
	c, err := r.Cookie(passcodeCookieName(r, siteID))
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return false
	}
	exp, err1 := strconv.ParseInt(parts[1], 10, 64)
	g, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || g != gen || exp < h.Passcode.clock().Unix() {
		return false
	}
	want := h.Passcode.cookieMAC(siteID, httpx.HostName(r), exp, g)
	return hmac.Equal([]byte(parts[3]), []byte(want))
}

// crossSiteSubresource: a request some other site's page made for a file
// (a <script>, <img>, fetch), not a navigation. Fetch metadata is set by
// browsers only; a request without it is a navigation or a non-browser.
func crossSiteSubresource(r *http.Request) bool {
	sfs := r.Header.Get("Sec-Fetch-Site")
	if sfs == "" || sfs == "same-origin" || sfs == "none" {
		return false
	}
	return r.Header.Get("Sec-Fetch-Mode") != "navigate"
}
