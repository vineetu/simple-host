package handler

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// "Your own address is on its way" (completeness plan, 2026-09-27).
//
// Until a person's *.<handle>.<SITE_DOMAIN> certificate is ready, their sites
// are handed out at <handle>.<SITE_DOMAIN>/<site>/ (sitehost.go). This reads
// the site-certs issuer's hand-off directory (deploy/site-certs/) to say where
// that certificate is: ready, waiting in the queue (with a rough time from the
// issuer's weekly and daily caps and what it has issued lately), or failing
// (retried automatically). Nothing here asks for a certificate; that is
// RequestSiteCert.

// Issuer limits: the defaults in deploy/site-certs/issue.sh, overridden by
// the limits file the issuer writes on every run (SITE_CERT_DIR/limits).
type siteCertLimits struct {
	Budget  int           // new certificates per rolling 7 days
	Daily   int           // new certificates per rolling 24 hours
	PerRun  int           // new certificates per run
	Every   time.Duration // how often the issuer runs (its timer)
	RetryIn time.Duration // wait after a failure
}

var defaultSiteCertLimits = siteCertLimits{Budget: 40, Daily: 12, PerRun: 6, Every: 10 * time.Minute, RetryIn: 6 * time.Hour}

// addressState is the account's own-address state (GET /v1/me "address",
// and "address_state" on a site still at its interim address).
type addressState struct {
	// State: ready (sites are at https://<site>.<handle>.<SITE_DOMAIN>/),
	// waiting (the certificate is queued) or failing (it could not be issued
	// yet and is retried automatically).
	State string `json:"state"`
	// Address is the pattern of the person's site addresses once ready.
	Address string `json:"address"`
	// InterimAddress is where sites are served until then.
	InterimAddress string `json:"interim_address,omitempty"`
	// ReadyBy / ReadyInHours: a rough estimate (waiting or failing only).
	ReadyBy      *time.Time `json:"ready_by,omitempty"`
	ReadyInHours *int       `json:"ready_in_hours,omitempty"`
	// Note says the same in plain words, including what changes on the switch.
	Note string `json:"note,omitempty"`
}

// addressCache keeps each handle's state for a short while: a site list
// computes it once, not once per site.
type addressCacheEntry struct {
	at    time.Time
	state *addressState
}

var addressCache sync.Map // handle -> addressCacheEntry

const addressCacheTTL = 30 * time.Second

// AddressState is siteAddressState for GET /v1/me.
func (h *SiteHandler) AddressState(handle string) *addressState { return h.siteAddressState(handle) }

// siteAddressState is the own-address state for an account, or nil when it
// does not apply: site hosts are not the canonical address, there is no
// hand-off directory, or the handle cannot be a host.
func (h *SiteHandler) siteAddressState(handle string) *addressState {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" || !h.siteHostsCanonical() || h.siteCertDir == "" || !handleAddressable(handle) {
		return nil
	}
	// Ready is a single file check and never cached: the address handed out
	// (siteHostLive) reads the same file.
	if h.siteCertReady(handle) {
		return &addressState{State: "ready", Address: "https://<site>." + h.personHostFor(handle) + "/"}
	}
	if e, ok := addressCache.Load(handle); ok {
		if ent := e.(addressCacheEntry); time.Since(ent.at) < addressCacheTTL {
			return ent.state
		}
	}
	st := h.computeAddressState(handle, time.Now())
	addressCache.Store(handle, addressCacheEntry{at: time.Now(), state: st})
	return st
}

func (h *SiteHandler) computeAddressState(handle string, now time.Time) *addressState {
	st := &addressState{
		State:          "ready",
		Address:        "https://<site>." + h.personHostFor(handle) + "/",
		InterimAddress: "https://" + h.personHostFor(handle) + "/<site>/",
	}
	if h.siteCertReady(handle) {
		st.InterimAddress = ""
		return st
	}
	limits := readSiteCertLimits(h.siteCertDir)
	var eta time.Time
	failedAt, failed := fileModTime(filepath.Join(h.siteCertDir, "failed", handle))
	if failed && now.Sub(failedAt) < limits.RetryIn {
		st.State = "failing"
		eta = failedAt.Add(limits.RetryIn).Add(limits.Every)
	} else {
		st.State = "waiting"
		ahead := siteCertQueueAhead(h.siteCertDir, handle, now, limits)
		eta = estimateCertReady(readIssuedLog(h.siteCertDir, now), ahead, now, limits)
	}
	hours := int(math.Ceil(eta.Sub(now).Hours()))
	if hours < 1 {
		hours = 1
	}
	eta = eta.UTC().Truncate(time.Minute)
	st.ReadyBy, st.ReadyInHours = &eta, &hours
	about := "about " + strconv.Itoa(hours) + " hour"
	if hours != 1 {
		about += "s"
	}
	if hours <= 1 {
		about = "within the hour"
	}
	lead := "Your sites are at " + h.personHostFor(handle) + "/<site>/ until your own address (<site>." + h.personHostFor(handle) + ") is ready — " + about
	if st.State == "failing" {
		lead = "Your sites are at " + h.personHostFor(handle) + "/<site>/ for now: the certificate for your own address (<site>." + h.personHostFor(handle) + ") could not be issued yet and is retried automatically — " + about
	}
	st.Note = lead + ". When it switches, links to the old address keep working (they redirect), but visitors' sign-ins and anything their browser kept for the site start fresh."
	return st
}

// siteAddressStateFor is the state to show on one site: only while that site
// is still handed out at its interim address.
func (h *SiteHandler) siteAddressStateFor(handle, name string) *addressState {
	if handle == "" || !h.personAddressFor(handle, name) || !validSiteName.MatchString(name) || strings.HasPrefix(name, "xn--") {
		return nil
	}
	st := h.siteAddressState(handle)
	if st == nil || st.State == "ready" {
		return nil
	}
	out := *st
	out.Address = "https://" + h.siteHostFor(handle, name) + "/"
	out.InterimAddress = h.personPathAddress(handle, name)
	return &out
}

func fileModTime(p string) (time.Time, bool) {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// readSiteCertLimits reads the limits file the issuer writes (KEY=value per
// line); anything missing keeps the default.
func readSiteCertLimits(dir string) siteCertLimits {
	l := defaultSiteCertLimits
	f, err := os.Open(filepath.Join(dir, "limits"))
	if err != nil {
		return l
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n <= 0 {
			continue
		}
		switch strings.TrimSpace(k) {
		case "BUDGET":
			l.Budget = n
		case "DAILY":
			l.Daily = n
		case "PER_RUN":
			l.PerRun = n
		case "RETRY_AFTER":
			l.RetryIn = time.Duration(n) * time.Second
		}
	}
	return l
}

// readIssuedLog is when the issuer issued each certificate in the last week
// ("<unix> <handle>" per line).
func readIssuedLog(dir string, now time.Time) []time.Time {
	f, err := os.Open(filepath.Join(dir, "issued.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []time.Time
	week := now.Add(-7 * 24 * time.Hour)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ts, _, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		n, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			continue
		}
		if t := time.Unix(n, 0); t.After(week) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// siteCertQueueAhead counts the requests the issuer takes before handle's
// (it goes oldest first and skips failed ones until their wait is over). A
// handle with no request yet is counted at the back of the queue.
func siteCertQueueAhead(dir, handle string, now time.Time, l siteCertLimits) int {
	entries, err := os.ReadDir(filepath.Join(dir, "requests"))
	if err != nil {
		return 0
	}
	mine, haveMine := fileModTime(filepath.Join(dir, "requests", handle))
	ahead := 0
	for _, e := range entries {
		name := e.Name()
		if name == handle || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if haveMine && !info.ModTime().Before(mine) {
			continue
		}
		if at, failed := fileModTime(filepath.Join(dir, "failed", name)); failed && now.Sub(at) < l.RetryIn {
			continue
		}
		ahead++
	}
	return ahead
}

// estimateCertReady is when the issuer can issue the certificate that has
// ahead others before it in the queue: each takes the first moment both the
// weekly budget and the daily cap have room, then one timer interval to run.
func estimateCertReady(issued []time.Time, ahead int, now time.Time, l siteCertLimits) time.Time {
	sim := append([]time.Time(nil), issued...)
	t := now
	for k := 0; k <= ahead; k++ {
		t = nextIssueSlot(sim, t, l)
		sim = append(sim, t)
	}
	return t.Add(l.Every)
}

// nextIssueSlot is the first moment at or after from when fewer than Budget
// certificates fall in the last 7 days and fewer than Daily in the last day.
func nextIssueSlot(issued []time.Time, from time.Time, l siteCertLimits) time.Time {
	room := func(t time.Time) bool {
		week, day := 0, 0
		for _, e := range issued {
			if e.After(t.Add(-7*24*time.Hour)) && !e.After(t) {
				week++
			}
			if e.After(t.Add(-24*time.Hour)) && !e.After(t) {
				day++
			}
		}
		return week < l.Budget && day < l.Daily
	}
	if room(from) {
		return from
	}
	// Room opens only when an issue leaves a window.
	var cands []time.Time
	for _, e := range issued {
		for _, c := range []time.Time{e.Add(24 * time.Hour), e.Add(7 * 24 * time.Hour)} {
			if c.After(from) {
				cands = append(cands, c.Add(time.Second))
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
	for _, c := range cands {
		if room(c) {
			return c
		}
	}
	return from.Add(7 * 24 * time.Hour)
}

// String is for logs and tests.
func (a *addressState) String() string {
	if a == nil {
		return "<nil>"
	}
	h := -1
	if a.ReadyInHours != nil {
		h = *a.ReadyInHours
	}
	return fmt.Sprintf("%s(%dh)", a.State, h)
}
