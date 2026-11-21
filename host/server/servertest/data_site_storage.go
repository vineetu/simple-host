package servertest

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// Shared storage fixtures: a site with the owner, two visitors (alice, bob),
// someone signed out and the owner signed in on the site.
type StorageTestSite struct {
	A                *PrivateApp
	Host, Sid        string
	Key              map[string]string
	Ids              map[string]string // caller -> account id
	Callers          map[string]map[string]string
	Owner            Person
	AliceID, OwnerID string
}

func NewStorageTestSite(t *testing.T, eds ...*edition.Edition) StorageTestSite {
	t.Helper()
	a, cert := NewSiteApp(t, "canonical", eds...)
	owner := a.NewPerson(t, "preset-owner")
	alice := a.NewPerson(t, "preset-alice")
	bob := a.NewPerson(t, "preset-bob")
	a.Deploy(t, owner, "shop")
	oid, handle := a.UserID(t, owner)
	aid, _ := a.UserID(t, alice)
	bid, _ := a.UserID(t, bob)
	MarkReady(t, cert, handle)
	host := "shop." + handle + "." + PcSiteDomain
	sid := a.SiteID(t, owner, "shop")
	// The grid sends hundreds of writes from one address.
	a.Data.StorageIPLimiter = httpx.NewRateLimiter(100000, 0)
	a.Data.StorageVisitorLimiter = httpx.NewRateLimiter(100000, 0)
	return StorageTestSite{
		A: a, Host: host, Sid: sid, Key: map[string]string{"X-API-Key": owner.Key}, Owner: owner,
		Ids: map[string]string{"alice": aid, "bob": bid, "owner-site": oid},
		Callers: map[string]map[string]string{
			"alice":      Browser(host, a.Session(t, alice, sid, host)),
			"bob":        Browser(host, a.Session(t, bob, sid, host)),
			"anon":       Browser(host, ""),
			"owner-site": Browser(host, a.Session(t, owner, sid, host)),
		},
		AliceID: aid, OwnerID: oid,
	}
}

func (s StorageTestSite) Do(t *testing.T, method, path string, body any, headers map[string]string) Resp {
	t.Helper()
	host := s.Host
	if headers["X-API-Key"] != "" {
		host = PcSiteDomain
	}
	return s.A.At(t, method, host, "/v1/sites/shop/storage"+path, body, headers)
}

func (s StorageTestSite) Must(t *testing.T, method, path string, body any, headers map[string]string, status int) Resp {
	t.Helper()
	r := s.Do(t, method, path, body, headers)
	if r.Status != status {
		t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
	}
	return r
}

func StorageKVFixtureCall(t *testing.T, h *datahttp.DataHandler, siteID, resource string) datahttp.StorageCall {
	t.Helper()
	c := datahttp.StorageCall{SiteID: siteID, ResourceName: resource}
	if err := h.Database.QueryRow(`SELECT user_id,name FROM sites WHERE id=$1`, siteID).Scan(&c.OwnerID, &c.SiteName); err != nil {
		t.Fatal(err)
	}
	var err error
	c.Resource, err = h.LoadStorageResource(context.Background(), siteID, resource)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func SeedStorageKV(t *testing.T, h *datahttp.DataHandler, siteID, resource, key, value, writer string) {
	t.Helper()
	c := StorageKVFixtureCall(t, h, siteID, resource)
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.StorageRuntimeDir(c), resource), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err = conn.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err = runtime.RuntimeKVPut(conn, key, value, writer, false, 0); err != nil {
		t.Fatal(err)
	}
	if err = conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
}

func ReadStorageKVAuthor(t *testing.T, h *datahttp.DataHandler, siteID, resource, key string) string {
	t.Helper()
	c := StorageKVFixtureCall(t, h, siteID, resource)
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.StorageRuntimeDir(c), resource), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return item.VisitorID
}

func ReadStorageKVValue(t *testing.T, h *datahttp.DataHandler, c datahttp.StorageCall, key string) string {
	t.Helper()
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.StorageRuntimeDir(c), c.ResourceName), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return string(item.Value)
}

// Rule sets with the same access as the removed presets of the same name.
const (
	RulesPublic   = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesInbox    = `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesWall     = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]}`
	RulesRecords  = `{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesPersonal = `{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]}`
	RulesBoard    = `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.signed_in"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.signed_in"},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	// Older read/write field pairs on key-value and SQLite, as rules.
	RulesOpenAdd              = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesSignedInWrite        = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.signed_in"},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesSignedInAdd          = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesOwnerReadSignedInAdd = `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	RulesComments             = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.checked || auth.ai || auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`
	// comments with approve_first: visitors see a post once the owner approves it.
	RulesCommentsApproveFirst = `{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.checked || auth.ai || auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""},{"name":"approve_first","command":"read","using":"auth.role == \"owner\" || approved_at is not null","restrictive":true}]}`
)

// storageRules is a resource body with the given rule set.
func StorageRules(kind, rules string) string {
	return `{"kind":"` + kind + `","rules":` + rules + `}`
}
