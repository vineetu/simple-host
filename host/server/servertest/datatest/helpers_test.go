package datatest

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"strconv"
	"testing"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Rule sets with the same access as the removed presets of the same name.
const (
	RulesPublic   = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesInbox    = `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesWall     = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]}`
	RulesRecords  = `{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesPersonal = `{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]}`
	RulesBoard    = `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.signed_in"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.signed_in"},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`

	RulesOpenAdd       = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesSignedInWrite = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.signed_in"},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesSignedInAdd   = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesComments      = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.checked || auth.ai || auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	// comments with approve_first: visitors see a post once the owner approves it.
	RulesCommentsApproveFirst = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.checked || auth.ai || auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""},{"name":"approve_first","command":"read","using":"auth.role == \"owner\" || approved_at is not null","restrictive":true}]}`
)

// StorageRules is a resource body with the given rule set.
func StorageRules(kind, rules string) string {
	return `{"kind":"` + kind + `","rules":` + rules + `}`
}

// Shop is the owner's site "shop" with two visitors (alice, bob) signed in on
// it, someone signed out (anon) and the owner signed in on it (owner-site).
type Shop struct {
	A                *App
	Host, Sid        string
	Key              map[string]string // the owner's key
	Owner            rt.Person
	Ids              map[string]string // caller -> account id
	Callers          map[string]map[string]string
	AliceID, OwnerID string
}

// NewShop deploys the shop and signs its callers in.
func NewShop(t *testing.T, eds ...*edition.Edition) Shop {
	t.Helper()
	a := NewApp(t, eds...)
	return a.NewShop(t)
}

// NewShop is NewShop on an existing app.
func (a *App) NewShop(t *testing.T) Shop {
	t.Helper()
	owner, alice, bob := a.NewPerson(t, "shop-owner"), a.NewPerson(t, "shop-alice"), a.NewPerson(t, "shop-bob")
	sid := a.Deploy(t, owner, "shop")
	host := owner.SiteHost("shop")
	return Shop{
		A: a, Host: host, Sid: sid, Key: owner.Header(), Owner: owner,
		Ids: map[string]string{"alice": alice.ID, "bob": bob.ID, "owner-site": owner.ID},
		Callers: map[string]map[string]string{
			"alice":      rt.Browser(host, a.Session(t, alice, sid, host)),
			"bob":        rt.Browser(host, a.Session(t, bob, sid, host)),
			"anon":       rt.Browser(host, ""),
			"owner-site": rt.Browser(host, a.Session(t, owner, sid, host)),
		},
		AliceID: alice.ID, OwnerID: owner.ID,
	}
}

// Do sends one request to the shop's storage routes: with the owner's key
// on the platform host, otherwise on the site's host.
func (s Shop) Do(t *testing.T, method, path string, body any, headers map[string]string) rt.Resp {
	t.Helper()
	host := s.Host
	if headers["X-API-Key"] != "" {
		host = rt.SiteDomain
	}
	return s.A.At(t, method, host, "/v1/sites/shop/storage"+path, body, headers)
}

// Must is Do that fails unless the answer is status.
func (s Shop) Must(t *testing.T, method, path string, body any, headers map[string]string, status int) rt.Resp {
	t.Helper()
	r := s.Do(t, method, path, body, headers)
	if r.Status != status {
		t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
	}
	return r
}

// AppHost is the test server's own address (the app's PUBLIC_BASE_URL host).
func (a *App) AppHost() string {
	u, _ := url.Parse(a.Srv.URL)
	return u.Host
}

// Cloudflare's documented test keys; siteverify itself is a local fake.
const (
	TurnstileTestSiteKey = "1x00000000000000000000AA"
	TurnstileTestSecret  = "1x0000000000000000000000000000000AA"
	TurnstileTestApp     = "https://app.simple-host.test"
)

var tokenSeq int

// Token is a spam-check token the fake siteverify passes for resource.
func (s Shop) Token(resource string) string {
	tokenSeq++
	return "ok-" + s.Host + "|" + resource + "|" + strconv.Itoa(tokenSeq)
}

// FakePushKeys is a plausible P-256 point and auth secret: enough for the
// RFC 8291 encryption to run against a test push endpoint.
func FakePushKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point := elliptic.Marshal(elliptic.P256(), x, y)
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(point), base64.RawURLEncoding.EncodeToString(authBytes)
}

// PushSubscribeBody is a push subscription for endpoint (events matter only
// to an owner subscription; a visitor's are always the record events).
func PushSubscribeBody(t *testing.T, endpoint, resource string) map[string]any {
	t.Helper()
	p256dh, auth := FakePushKeys(t)
	body := map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": p256dh, "auth": auth}, "label": "test device", "events": append([]string{}, notify.RecordEventNames...)}
	if resource != "" {
		body["resource"] = resource
	}
	return body
}
