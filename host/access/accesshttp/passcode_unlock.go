package accesshttp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// --- Unlocking ---

// UnlockSite is POST /v1/site-unlock: the gate's form (passcode, next). It
// works only from a page on this very host, for the site this host (or, on
// a person host, next) names; a right passcode sets the unlock cookie and
// 303s back to next, a wrong one shows the gate again with the reason.
// Wrong tries are limited per site and address and per site (wrongTry
// below). The passcode is never logged.
func (h *AccessHandler) UnlockSite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bad request")
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "the passcode is entered on the site's own page", Code: "origin_not_allowed"})
		return
	}
	host := httpx.HostName(r)
	userID, name, ok, err := h.passcodeSiteForHost(r.Context(), host, next)
	if err != nil {
		log.Printf("passcode unlock %s: %v", host, err)
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	var row model.SitePasscodeRow
	if ok {
		row, err = h.sitesSvc().GetSitePasscodeByName(r.Context(), userID, name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("passcode unlock %s: %v", host, err)
			web.RenderServiceErrorPage(w, r, h.pipeline())
			return
		}
	}
	if !ok || err != nil || row.Enc == nil {
		// Nothing to unlock here: back to the page, which shows whatever
		// it shows.
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	// The gate always posts from the site's own page, so its response is
	// never stored either.
	wait, matched, err := h.CheckVisitorPasscode(r, row, r.PostForm.Get("passcode"))
	if err != nil {
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	if wait > 0 {
		h.servePasscodeGate(w, r, next, "Too many tries. Try again in "+web.LimitSpan(roundUpMinute(wait))+".", wait)
		return
	}
	if !matched {
		h.servePasscodeGate(w, r, next, "That passcode isn’t right. Check it and try again.", 0)
		return
	}
	h.setUnlockCookie(w, r, row.SiteID, row.Generation)
	log.Printf("passcode_unlock ok=true site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func roundUpMinute(d time.Duration) time.Duration {
	if d < time.Minute {
		return time.Minute
	}
	return ((d + time.Minute - 1) / time.Minute) * time.Minute
}

// ipHash is a short, per-process salted hash of a client address for the
// log (never the address itself).
func (p *passcodeState) ipHash(ip string) string {
	p.mu.Lock()
	if p.ipSalt == nil {
		p.ipSalt = make([]byte, 16)
		_, _ = rand.Read(p.ipSalt)
	}
	salt := p.ipSalt
	p.mu.Unlock()
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil)[:6])
}

// lockedFor reports how long unlocking is refused: the site as a whole (its
// wrong tries from everyone used up RATE_LIMIT_PASSCODE_SITE) or this
// address on it (RATE_LIMIT_PASSCODE_IP). 0 = not refused.
func (p *passcodeState) lockedFor(siteID, ipKey string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	return max(p.locked["site:"+siteID].Sub(now), p.locked["ip:"+ipKey].Sub(now), 0)
}

// wrongTry counts a wrong passcode against the address and the site and
// returns the lockout it started (0 if none).
func (p *passcodeState) wrongTry(siteID, ipKey string) time.Duration {
	lim := p.Limits
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ipTries == nil {
		p.ipTries = httpx.NewRateLimiter(float64(lim.RatePasscodeIP.Burst), lim.RatePasscodeIP.PerSecond())
		p.siteTries = httpx.NewRateLimiter(float64(lim.RatePasscodeSite.Burst), lim.RatePasscodeSite.PerSecond())
		p.ipTries.Now, p.siteTries.Now = p.clock, p.clock
		p.locked = map[string]time.Time{}
	}
	now := p.clock()
	if p.ipTries.BucketCount() >= 4096 {
		p.sweep(now)
	}
	var wait time.Duration
	// The try that empties a bucket starts its lockout.
	if _, left, _ := p.ipTries.AllowState(ipKey); left < 1 {
		p.locked["ip:"+ipKey] = now.Add(lim.PasscodeLockout)
		wait = lim.PasscodeLockout
	}
	if _, left, _ := p.siteTries.AllowState(siteID); left < 1 {
		p.locked["site:"+siteID] = now.Add(lim.PasscodeSiteLockout)
		log.Printf("passcode_site_lockout site_id=%s minutes=%d", siteID, int(lim.PasscodeSiteLockout.Minutes()))
		wait = max(wait, lim.PasscodeSiteLockout)
	}
	return wait
}

// evict drops refilled buckets and ended lockouts, so a burst of wrong tries
// from many addresses does not stay in memory after it ends.
func (p *passcodeState) evict() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sweep(p.clock())
}

// sweep is evict with p.mu held.
func (p *passcodeState) sweep(now time.Time) int {
	if p.ipTries == nil {
		return 0
	}
	removed := p.ipTries.EvictIdle(0) + p.siteTries.EvictIdle(0)
	for k, until := range p.locked {
		if !now.Before(until) {
			delete(p.locked, k)
			removed++
		}
	}
	return removed
}

// CheckVisitorPasscode is shared by the browser unlock and visitor tools.
// AI calls use a person bucket and a separate site budget. Browsers use IPs.
func (h *AccessHandler) CheckVisitorPasscode(r *http.Request, row model.SitePasscodeRow, supplied string) (time.Duration, bool, error) {
	siteBucket := row.SiteID
	ipKey := row.SiteID + "|" + httpx.ClientIP(r)
	if pass, ok := h.StoreAccounts().LookupInternalVisitorKey(r.Header.Get(VisitorPassHeader)); ok && pass.SiteID == row.SiteID {
		siteBucket = "ai:" + row.SiteID
		ipKey = siteBucket + "|" + pass.UserID
	}
	if wait := h.Passcode.lockedFor(siteBucket, ipKey); wait > 0 {
		log.Printf("passcode_unlock ok=false reason=locked_out site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
		return wait, false, nil
	}
	code, err := h.Passcode.open(row.SiteID, row.Enc)
	if err != nil {
		log.Printf("passcode unlock site_id=%s: %v", row.SiteID, err)
		return 0, false, err
	}
	if !passcodeMatches(strings.TrimSpace(supplied), code) {
		wait := h.Passcode.wrongTry(siteBucket, ipKey)
		log.Printf("passcode_unlock ok=false site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
		return wait, false, nil
	}
	return 0, true, nil
}
