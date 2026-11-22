package sitestest

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"testing"

	operations "github.com/vsriram/simple-host/host/ops"
)

// The owner's own browser (Sec-Fetch-Site: same-origin) on the site list or
// analytics writes one throttled mark with the ingester's hash of the client
// address; a plain API call or a cross-site read writes none.
func TestOwnerBrowserMark(t *testing.T) {
	a := NewApp(t)
	a.Sites.SetOwnerMarks(operations.NewOwnerMarks(a.Database, "k", "t"))
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	count := func() int {
		t.Helper()
		var n int
		if err := a.Database.QueryRow(`SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, olive.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	get := func(path, fetchSite, ip string) {
		t.Helper()
		h := map[string]string{"X-API-Key": olive.Key, "X-Forwarded-For": ip}
		if fetchSite != "" {
			h["Sec-Fetch-Site"] = fetchSite
		}
		if r := a.At(t, "GET", "simple-host.test", path, nil, h); r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
	}
	get("/v1/sites", "", "198.51.100.7")
	if n := count(); n != 0 {
		t.Fatalf("plain API call wrote %d marks", n)
	}
	get("/v1/sites", "same-origin", "198.51.100.7")
	get("/v1/sites/shop/analytics?days=7", "same-origin", "198.51.100.7")
	get("/v1/sites/shop/analytics?days=7", "cross-site", "198.51.100.8")
	if n := count(); n != 1 {
		t.Fatalf("browser reads left %d marks, want 1", n)
	}
	var hash []byte
	if err := a.Database.QueryRow(`SELECT ip_hash FROM owner_visitor_marks WHERE user_id = $1`, olive.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256([]byte("t" + "198.51.100.7")); !bytes.Equal(hash, sum[:16]) {
		t.Fatal("mark hash is not HashIP(salt, client address)")
	}
}
