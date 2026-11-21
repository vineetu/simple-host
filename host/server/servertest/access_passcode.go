package servertest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

const TestPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80=" // 32 bytes, tests only

// withPasscodeLimits changes config.Limits for the rest of the test on every
// handler's own snapshot (targets, each a pointer to a *SiteHandler's
// limits field, e.g. &a.sites.limits, &a.access.limits, &a.data.limits), seeded from the first
// target's current values, and restores each afterwards. There is no global
// left to mutate, so every target that matters to the test must be named.
func WithPasscodeLimits(t *testing.T, change func(l *config.Limits), targets ...*edition.Limits) {
	t.Helper()
	if len(targets) == 0 {
		t.Fatal("withPasscodeLimits: no target handler given")
	}
	l := *targets[0]
	change(&l)
	olds := make([]edition.Limits, len(targets))
	for i, tgt := range targets {
		olds[i] = *tgt
		*tgt = l
	}
	t.Cleanup(func() {
		for i, tgt := range targets {
			*tgt = olds[i]
		}
	})
}

// passcodeApp is a canonical site-host router with passcodes on, olive's
// site "trip" deployed with a canary in every kind of file, and olive's
// certificate ready.
type PasscodeApp struct {
	*PrivateApp
	Olive           Person
	Handle, SiteID  string
	Host, Person    string
	Okey            map[string]string
	canary, PageDir string
}

const PasscodeCanary = "CANARY-7f3a91-do-not-leak"

// passcodeData is a saved-data read on the fixture's site (a public KV value
// holding the canary).
const PasscodeData = "/v1/sites/trip/storage/kv/notes/keys/c"

func NewPasscodeApp(t *testing.T) *PasscodeApp {
	t.Helper()
	a, dir := NewSiteApp(t, "canonical")
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	p := &PasscodeApp{PrivateApp: a, Olive: a.NewPerson(t, "olive"), canary: PasscodeCanary}
	_, p.Handle = a.UserID(t, p.Olive)
	MarkReady(t, dir, p.Handle)
	p.Okey = map[string]string{"X-API-Key": p.Olive.Key}
	r := a.At(t, "POST", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{
		"index.html":      "<h1>trip " + PasscodeCanary + "</h1>",
		"app.js":          "var s='" + PasscodeCanary + "';",
		"data.json":       `{"x":"` + PasscodeCanary + `"}`,
		"img.svg":         `<svg xmlns="http://www.w3.org/2000/svg"><text>` + PasscodeCanary + `</text></svg>`,
		"days/index.html": "day " + PasscodeCanary,
		"404.html":        "missing " + PasscodeCanary,
		"robots.txt":      "User-agent: *\nAllow: /\n# " + PasscodeCanary,
	}}, p.Okey)
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/storage/resources/notes", StorageRules("kv", RulesPublic), p.Okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, PasscodeData, map[string]any{"value": PasscodeCanary}, p.Okey); r.Status != 200 {
		t.Fatalf("storage value: %d %s", r.Status, r.Body)
	}
	p.SiteID = a.SiteID(t, p.Olive, "trip")
	p.Person = p.Handle + "." + PcSiteDomain
	p.Host = "trip." + p.Person
	uid, _ := a.UserID(t, p.Olive)
	p.PageDir = a.Sites.Disk.SiteDir(uid, "trip")
	return p
}

func (p *PasscodeApp) Lock(t *testing.T, code string) Resp {
	t.Helper()
	return p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": code}, p.Okey)
}

// unlock posts the gate's form from host and returns the answer.
func (p *PasscodeApp) Unlock(t *testing.T, host, code, next string, extra map[string]string) Resp {
	t.Helper()
	form := url.Values{"passcode": {code}, "next": {next}}.Encode()
	h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
	for k, v := range extra {
		h[k] = v
	}
	return p.At(t, "POST", host, "/v1/site-unlock", form, h)
}

func CookieHeader(t *testing.T, r Resp) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if strings.Contains(c.Name, "sh_pass_") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no unlock cookie in %v", r.Header)
	return ""
}
