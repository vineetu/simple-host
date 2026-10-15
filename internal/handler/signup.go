package handler

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Where an account came from (users.signup_*), recorded once when it is
// created and shown on the admin page. It is a label, not a permission:
// nothing reads it to decide anything, so a caller that lies about itself only
// mislabels its own account. Never an IP address.

// signupAgentMax bounds the agent name kept for an API sign-up.
const signupAgentMax = 40

// recordSignup stores s for a just-created account. A failure is logged and
// never fails the sign-in: the label is not worth refusing a person over.
func recordSignup(ctx context.Context, q db.Querier, userID string, s db.Signup) {
	if err := db.SetSignup(ctx, q, userID, s); err != nil {
		log.Printf("signup: record source user_id=%s: %v", userID, err)
	}
}

// signupForVerify says where an account created by POST /v1/auth/verify came
// from: the /mcp consent page (it names the app's client_id), one of the
// site's own pages (a same-site browser request), or an agent calling the API.
func signupForVerify(ctx context.Context, q db.Querier, r *http.Request, publicBaseURL, clientID string, viaLink bool) db.Signup {
	method := db.SignupMethodEmail
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		if s, ok := connectorSignup(ctx, q, clientID, method); ok {
			return withArrival(r, s)
		}
	}
	if viaLink || fromOwnPages(r, publicBaseURL) {
		return websiteSignup(r, method)
	}
	return db.Signup{Source: db.SignupAgent, Agent: signupAgentName(r), Method: method}
}

// connectorSignup names the app registered as clientID.
func connectorSignup(ctx context.Context, q db.Querier, clientID, method string) (db.Signup, bool) {
	c, err := db.GetOAuthClient(ctx, q, clientID)
	if err != nil {
		return db.Signup{}, false
	}
	return db.Signup{Source: db.SignupConnectorPrefix + cleanClientName(c.Name), Method: method}, true
}

// signupForOwnerReturnTo says where a Google or GitHub sign-up that started on one of
// the site's pages came from: the consent page (/oauth/authorize, whose query
// names the app) or anywhere else on the site.
func signupForOwnerReturnTo(ctx context.Context, q db.Querier, r *http.Request, returnTo, provider string) db.Signup {
	if u, err := url.Parse(returnTo); err == nil && u.Path == "/oauth/authorize" {
		if s, ok := connectorSignup(ctx, q, u.Query().Get("client_id"), provider); ok {
			return withArrival(r, s)
		}
	}
	return websiteSignup(r, provider)
}

// arrivalCookie remembers the outside link that first brought a browser to
// the apex (/?from=<value>), so a sign-up in the next 30 days counts for it
// even when it happens in another tab, later, or through the connector's
// consent page (INTENT 2026-10-15). First-party and host-only, HttpOnly, set
// by the server. __Host- because a hosted site on a sibling host can plant a
// Domain cookie of a plain name, never a __Host- one, and the person and site
// hosts under the apex never see it. It holds the from value, the utm_*
// parameters, the linking site's host and the landing time; never an IP. It
// is a label and nothing else reads it.
const arrivalCookie = "__Host-sh_from"

// arrivalWindow is how long a landing counts: first touch within 30 days.
const arrivalWindow = 30 * 24 * time.Hour

// arrivalSource names an outside place that links people in with
// /?from=<key>. Where is the address the link lives on, for the admin page.
type arrivalSource struct {
	Label string `json:"label"`
	Where string `json:"where"`
}

// arrivalSources is the one list of named sources: the key is both the
// /?from= value and the signup_source recorded. Add a partner by adding a
// line here; the admin page reads its labels from GET /v1/admin/users. Any
// other well-formed from value still counts, as "from:<value>".
var arrivalSources = map[string]arrivalSource{
	db.SignupArtifactHub: {Label: "Artifact Hub", Where: "artifact-hub.com"},
	db.SignupVercelShip:  {Label: "Vercel Ship", Where: "ship.vineetsriram.com"},
}

// arrivalUTM are the link parameters kept with a from value, as cookie keys.
var arrivalUTM = [...]struct{ param, key string }{
	{"utm_source", "us"}, {"utm_medium", "um"}, {"utm_campaign", "uc"}, {"utm_term", "ut"}, {"utm_content", "ux"},
}

// arrivalFrom accepts a from value: lower-case letters, digits and hyphens,
// at most 40 characters.
func arrivalFrom(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 40 || s[0] == '-' {
		return "", false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "", false
		}
	}
	return s, true
}

// arrivalText keeps a utm value short and plain: printable ASCII, no quotes
// or angle brackets, at most 80 characters.
func arrivalText(s string) string {
	var b strings.Builder
	for _, c := range strings.TrimSpace(s) {
		if c >= 0x20 && c < 0x7f && !strings.ContainsRune("\"'<>`", c) {
			b.WriteRune(c)
		}
		if b.Len() >= 80 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// arrivalReferrer is the host of the page that linked in, when it is another
// site; never the path or query, which can carry personal details.
func arrivalReferrer(r *http.Request) string {
	u, err := url.Parse(r.Referer())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || len(host) > 100 || strings.EqualFold(host, hostOnly(r.Host)) || looksLikeIP(host) {
		return ""
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return ""
		}
	}
	return host
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// readArrival is the browser's remembered landing, when it is still inside
// the window. The plain-named session cookie used before 2026-10-15 is not
// read: any sibling host could have planted it.
func readArrival(r *http.Request, now time.Time) (*db.SignupDetail, bool) {
	c, err := r.Cookie(arrivalCookie)
	if err != nil {
		return nil, false
	}
	v, err := url.ParseQuery(c.Value)
	if err != nil {
		return nil, false
	}
	from, ok := arrivalFrom(v.Get("f"))
	if !ok {
		return nil, false
	}
	sec, err := strconv.ParseInt(v.Get("t"), 10, 64)
	if err != nil {
		return nil, false
	}
	landed := time.Unix(sec, 0).UTC()
	if now.Sub(landed) > arrivalWindow || landed.After(now.Add(time.Hour)) {
		return nil, false
	}
	d := &db.SignupDetail{From: from, Referrer: arrivalText(v.Get("r")), LandedAt: landed,
		UTMSource: arrivalText(v.Get("us")), UTMMedium: arrivalText(v.Get("um")), UTMCampaign: arrivalText(v.Get("uc")),
		UTMTerm: arrivalText(v.Get("ut")), UTMContent: arrivalText(v.Get("ux"))}
	return d, true
}

// setArrivalCookie remembers, for 30 days, that the visitor arrived through
// /?from=<value>. First touch wins: a landing already remembered and still
// inside the window is kept, so a later link does not take the credit.
func setArrivalCookie(w http.ResponseWriter, r *http.Request) {
	from, ok := arrivalFrom(r.URL.Query().Get("from"))
	if !ok {
		return
	}
	now := time.Now()
	if _, ok := readArrival(r, now); ok {
		return
	}
	v := url.Values{"f": {from}, "t": {strconv.FormatInt(now.Unix(), 10)}}
	q := r.URL.Query()
	for _, u := range arrivalUTM {
		if x := arrivalText(q.Get(u.param)); x != "" {
			v.Set(u.key, x)
		}
	}
	if ref := arrivalReferrer(r); ref != "" {
		v.Set("r", ref)
	}
	http.SetCookie(w, &http.Cookie{Name: arrivalCookie, Value: v.Encode(), Path: "/",
		MaxAge: int(arrivalWindow / time.Second), Expires: now.Add(arrivalWindow),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// arrivalSourceOf is the signup_source a remembered from value records.
func arrivalSourceOf(from string) string {
	if _, ok := arrivalSources[from]; ok {
		return from
	}
	return db.SignupFromPrefix + from
}

// withArrival credits a browser sign-up (from the site's own pages or the
// connector's consent page) to the outside link this browser landed through
// in the last 30 days. The connected app's name moves to Agent, so it is not
// lost. Sources that say more (an agent's API call, a site visitor, an
// issued account) never come through here.
func withArrival(r *http.Request, s db.Signup) db.Signup {
	d, ok := readArrival(r, time.Now())
	if !ok {
		return s
	}
	if strings.HasPrefix(s.Source, db.SignupConnectorPrefix) {
		s.Agent = strings.TrimPrefix(s.Source, db.SignupConnectorPrefix)
	}
	s.Source = arrivalSourceOf(d.From)
	s.Detail = d
	return s
}

// websiteSignup is a sign-up from the site's own pages: credited to the
// remembered outside link when there is one, else "website".
func websiteSignup(r *http.Request, method string) db.Signup {
	return withArrival(r, db.Signup{Source: db.SignupWebsite, Method: method})
}

// fromOwnPages reports a browser request made by one of this site's pages.
// Browsers set Sec-Fetch-Site themselves (a page cannot); Origin is the
// fallback for one that does not send it.
func fromOwnPages(r *http.Request, publicBaseURL string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site":
		return true
	case "":
	default:
		return false
	}
	o := strings.TrimRight(r.Header.Get("Origin"), "/")
	return o != "" && publicBaseURL != "" && strings.EqualFold(o, strings.TrimRight(publicBaseURL, "/"))
}

// signupAgentName names the agent behind an API sign-up: the
// X-Simple-Host-Client header, else "skill <X-Skill-Version>", else the
// User-Agent's product token ("curl", "python-requests"). Printable ASCII
// only, at most signupAgentMax characters; "" when there is nothing to go on.
func signupAgentName(r *http.Request) string {
	if v := cleanAgentName(r.Header.Get("X-Simple-Host-Client")); v != "" {
		return v
	}
	if v := cleanAgentName(r.Header.Get("X-Skill-Version")); v != "" {
		return clipAgent("skill " + v)
	}
	ua := strings.TrimSpace(r.UserAgent())
	if i := strings.IndexAny(ua, "/ ("); i >= 0 {
		ua = ua[:i]
	}
	ua = cleanAgentName(ua)
	if looksLikeIP(ua) {
		return ""
	}
	if strings.EqualFold(ua, "Mozilla") {
		return "browser"
	}
	return ua
}

// cleanAgentName keeps letters, digits and . _ - + / : ( ) and single spaces.
// An IP address is dropped: no IP is ever kept.
func cleanAgentName(s string) string {
	s = strings.TrimSpace(s)
	if looksLikeIP(s) {
		return ""
	}
	var b strings.Builder
	space := false
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("._-+/:()", c):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(c)
		case c == ' ' || c == '\t':
			space = true
		}
	}
	return clipAgent(b.String())
}

func clipAgent(s string) string {
	if len(s) > signupAgentMax {
		s = strings.TrimSpace(s[:signupAgentMax])
	}
	return s
}

// looksLikeIP: an IP address, with or without a port or brackets.
func looksLikeIP(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	return net.ParseIP(strings.Trim(s, "[]")) != nil
}
