package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

// The spam check behind the comments preset (owner decision 2026-10-14,
// INTENT.md): anyone may add to a resource whose add is "checked" without
// signing in, once Cloudflare Turnstile says the browser passed its check.
// The check is Cloudflare's, not ours.
//
// Sites live on many hostnames (site hosts, families, custom domains) and a
// widget lists only a few, so the widget never runs on the site's page. It
// runs in a small frame served from this server's own address
// (GET /v1/turnstile/frame on PUBLIC_BASE_URL's host). auth.js embeds the
// frame, sends it the resource name with postMessage, and gets the token
// back the same way; both sides check the other's origin and window. The
// frame puts the page's origin and the resource into the token's cdata, so
// the server accepts a token only for a write to that resource on that host.
//
// The server calls siteverify itself and requires success, the action
// sh-post, the app's own hostname, that cdata, and a challenge under
// turnstileMaxAge old. Tokens are single-use at Cloudflare and here.
// Anything else, a timeout or an unreachable Cloudflare included, refuses
// the post (fail closed). The visitor's address is never sent (CLAUDE.md:
// visitor IPs never go to a third party).

const (
	turnstileAction     = "sh-post"
	turnstileMaxToken   = 2048
	turnstileMaxAge     = 300 * time.Second
	turnstileTimeout    = 10 * time.Second
	turnstileHeader     = "X-SH-Turnstile"
	turnstileSiteverify = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
)

// turnstileVerifyURL is siteverify; tests point it at a local server.
var turnstileVerifyURL = turnstileSiteverify

type turnstileVerifier struct {
	siteKey, secret string
	// hostname: the app's own host, where the frame runs; siteverify must
	// name exactly this one.
	hostname string
	client   *http.Client
	mu       sync.Mutex
	used     map[string]time.Time
	now      func() time.Time
}

func newTurnstileVerifier(siteKey, secret, publicBaseURL string) *turnstileVerifier {
	siteKey, secret = strings.TrimSpace(siteKey), strings.TrimSpace(secret)
	u, err := url.Parse(strings.TrimSpace(publicBaseURL))
	if siteKey == "" || secret == "" || err != nil || u.Hostname() == "" {
		return nil
	}
	return &turnstileVerifier{siteKey: siteKey, secret: secret, hostname: strings.ToLower(u.Hostname()),
		client: &http.Client{Timeout: turnstileTimeout}, used: map[string]time.Time{}, now: time.Now}
}

// SetTurnstile turns the spam check (and with it the comments preset) on.
// Empty values leave it off.
func (h *SiteHandler) SetTurnstile(siteKey, secret, publicBaseURL string) {
	h.turnstile = newTurnstileVerifier(siteKey, secret, publicBaseURL)
	if h.turnstile != nil {
		h.spamIPLimiter.startCleanup(10*time.Minute, 30*time.Minute)
		h.spamSiteLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	}
}

// SpamCheckEnabled: the comments preset is offered on this server.
func (h *SiteHandler) SpamCheckEnabled() bool { return h.turnstile != nil }

// turnstileCData binds a token to one host and one resource. Turnstile takes
// up to 255 letters, digits, _ and -, so it is a hash.
func turnstileCData(host, resource string) string {
	sum := sha256.Sum256([]byte("sh-turnstile\n" + strings.ToLower(host) + "\n" + resource))
	return hex.EncodeToString(sum[:16])
}

// spend records a token as used; false when it was used already. Entries
// outlive any token's lifetime, then go.
func (v *turnstileVerifier) spend(token string) bool {
	sum := sha256.Sum256([]byte(token))
	k := hex.EncodeToString(sum[:])
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	for t, at := range v.used {
		if now.Sub(at) > 2*turnstileMaxAge {
			delete(v.used, t)
		}
	}
	if _, seen := v.used[k]; seen {
		return false
	}
	v.used[k] = now
	return true
}

// verify answers "" when the token passes for host and resource, else a
// refusal code: spam_check_failed (the token is wrong, reused, old, or for
// somewhere else) or spam_check_unavailable (Cloudflare did not answer).
func (v *turnstileVerifier) verify(ctx context.Context, token, host, resource string) string {
	if token == "" || len(token) > turnstileMaxToken || !v.spend(token) {
		return "spam_check_failed"
	}
	form := url.Values{"secret": {v.secret}, "response": {token}}
	ctx, cancel := context.WithTimeout(ctx, turnstileTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "spam_check_unavailable"
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.client.Do(req)
	if err != nil {
		return "spam_check_unavailable"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "spam_check_unavailable"
	}
	var out struct {
		Success     bool     `json:"success"`
		Action      string   `json:"action"`
		Hostname    string   `json:"hostname"`
		CData       string   `json:"cdata"`
		ChallengeTS string   `json:"challenge_ts"`
		ErrorCodes  []string `json:"error-codes"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil {
		return "spam_check_unavailable"
	}
	if !out.Success || out.Action != turnstileAction || !strings.EqualFold(out.Hostname, v.hostname) ||
		out.CData != turnstileCData(host, resource) {
		return "spam_check_failed"
	}
	ts, err := time.Parse(time.RFC3339, out.ChallengeTS)
	if err != nil || v.now().Sub(ts) > turnstileMaxAge || ts.Sub(v.now()) > time.Minute {
		return "spam_check_failed"
	}
	return ""
}

// turnstileMessages are the plain words for each refusal.
var turnstileMessages = map[string]string{
	"spam_check_required":    "this needs the spam check first; auth.js runs it on its own when the page saves with SH.storage",
	"spam_check_failed":      "the spam check did not pass; try again",
	"spam_check_unavailable": "the spam check is not available right now, so nothing was saved; try again in a minute",
}

// storageSpamCheck runs the check once per request, before any lock, on a
// visitor's write to a resource some add of which is checked. The result
// waits on c for storageAllow.
func (h *SiteHandler) storageSpamCheck(w http.ResponseWriter, r *http.Request, c *storageCall) bool {
	token := r.Header.Get(turnstileHeader)
	// Without CSRF the gate refuses the write anyway: no Cloudflare call.
	if token == "" || c.owner || !storageVisitorWrite(r) || !c.resource.hasChecked() || r.Header.Get("X-SH-CSRF") != "1" {
		return true
	}
	if h.turnstile == nil {
		c.spamCheck = "spam_check_unavailable"
		return true
	}
	if !h.spamIPLimiter.allow(clientIP(r)) || !h.spamSiteLimiter.allow(c.siteID) {
		tooManyRequests(w)
		return false
	}
	if code := h.turnstile.verify(r.Context(), token, requestHostName(r), c.resourceName); code != "" {
		c.spamCheck = code
		return true
	}
	c.spamCheck = "ok"
	return true
}

// storageCheckedAllow decides a checked add for someone other than the
// owner.
func storageCheckedAllow(w http.ResponseWriter, c *storageCall) bool {
	code := c.spamCheck
	switch code {
	case "ok":
		c.checkedPost = true
		return true
	case "":
		code = "spam_check_required"
	}
	storageError(w, 403, code, turnstileMessages[code])
	return false
}

// storagePostLimits keeps a post from someone not signed in small and
// mostly free of links. raw is the JSON the page sent.
func storagePostLimits(w http.ResponseWriter, raw []byte) bool {
	lim := config.Active()
	if len(raw) > lim.SpamCheckedMaxBytes {
		storageError(w, 413, "post_too_large", fmt.Sprintf("a post here is at most %d bytes", lim.SpamCheckedMaxBytes))
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		storageError(w, 400, "invalid_value", "valid JSON required")
		return false
	}
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			b.WriteString(strings.ToLower(t))
			b.WriteByte(' ')
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for k, e := range t {
				walk(k)
				walk(e)
			}
		}
	}
	walk(v)
	text := b.String()
	links := strings.Count(text, "http://") + strings.Count(text, "https://") + strings.Count(text, "www.")
	if links > lim.SpamCheckedMaxLinks {
		storageError(w, 400, "too_many_links", fmt.Sprintf("a post here may hold at most %d links", lim.SpamCheckedMaxLinks))
		return false
	}
	return true
}

// turnstileFrame is GET /v1/turnstile/frame, on the app's own address only.
// It is the one page with Cloudflare's script, and the one page any origin
// may frame (frame-ancestors *, which browsers obey over the proxy's
// X-Frame-Options). It holds no session and reads no cookie.
func (h *SiteHandler) turnstileFrame(w http.ResponseWriter, r *http.Request) {
	if h.turnstile == nil || !strings.EqualFold(requestHostName(r), h.turnstile.hostname) {
		http.NotFound(w, r)
		return
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(b[:])
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+
		"script-src 'nonce-"+nonce+"' https://challenges.cloudflare.com; "+
		"frame-src https://challenges.cloudflare.com; "+
		"connect-src https://challenges.cloudflare.com; "+
		"style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors *")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	key, _ := json.Marshal(h.turnstile.siteKey)
	action, _ := json.Marshal(turnstileAction)
	fmt.Fprintf(w, turnstileFramePage, nonce, nonce, key, action)
}

// The frame: it starts only when its parent window posts
// {type: "sh-turnstile-start", resource}, binds the token to the parent's
// origin (the browser's event.origin, never a value the page sends) and the
// resource, and posts the token to that origin alone. Opened on its own, or
// from an opaque origin, it does nothing.
const turnstileFramePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Spam check</title>
<style>html,body{margin:0;padding:0;background:transparent;overflow:hidden}</style>
<script nonce="%s" src="https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit"></script>
</head><body><div id="w"></div>
<script nonce="%s">
(function () {
  "use strict";
  var SITE_KEY = %s, ACTION = %s, started = false;
  if (window.parent === window) return;
  function hex(buf) { return Array.prototype.map.call(new Uint8Array(buf), function (b) { return ("0" + b.toString(16)).slice(-2); }).join(""); }
  window.addEventListener("message", function (e) {
    if (started || e.source !== window.parent) return;
    var d = e.data;
    if (!d || d.type !== "sh-turnstile-start" || typeof d.resource !== "string" || !/^[a-z][a-z0-9_-]{0,63}$/.test(d.resource)) return;
    if (!/^https?:\/\/[^\/]+$/.test(e.origin)) return;
    started = true;
    var origin = e.origin, host = new URL(origin).hostname.toLowerCase();
    function send(msg) { window.parent.postMessage(msg, origin); }
    crypto.subtle.digest("SHA-256", new TextEncoder().encode("sh-turnstile\n" + host + "\n" + d.resource)).then(function (sum) {
      window.turnstile.render("#w", {
        sitekey: SITE_KEY, action: ACTION, cdata: hex(sum).slice(0, 32),
        callback: function (token) { send({type: "sh-turnstile-token", token: token}); },
        "error-callback": function (code) { send({type: "sh-turnstile-error", code: String(code || "")}); return true; },
        "expired-callback": function () { send({type: "sh-turnstile-error", code: "expired"}); },
        "timeout-callback": function () { send({type: "sh-turnstile-error", code: "timeout"}); }
      });
    }).catch(function () { send({type: "sh-turnstile-error", code: "unsupported"}); });
  });
  window.parent.postMessage({type: "sh-turnstile-ready"}, "*");
})();
</script></body></html>`
