package datatest

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/server/servertest"
)

type ownerBindingFixture struct {
	appstorage.NoBindings
	calls int
}

func (p *ownerBindingFixture) OwnerOnSite(context.Context, string, string) (bool, error) {
	p.calls++
	return true, nil
}

func TestStorageBindingRequestGuards(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	h := s.A.Data
	h.Ed = servertest.GroupTestEdition()
	h.Hosts.SetEdition(h.Ed)
	p := &ownerBindingFixture{}
	h.SetStorageBindings(p)
	preview := "https://" + s.Host + "/__preview/1/" + s.A.Sites.SignPreviewToken(s.Sid, 1, time.Now().Add(time.Hour)) + "/"
	for _, tc := range []struct {
		name                string
		ai, anonymous       bool
		host, header, value string
		want                bool
	}{
		{name: "own host", host: s.Host, want: true},
		{name: "visitor AI", host: s.Host, ai: true},
		{name: "anonymous", host: s.Host, anonymous: true},
		{name: "framed", host: s.Host, header: "X-SH-Framed", value: "1"},
		{name: "preview", host: s.Host, header: "Referer", value: preview},
		{name: "apex", host: servertest.PcSiteDomain},
		{name: "shared content", host: s.A.Sites.ContentHost},
		{name: "unrelated host", host: "unrelated.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://"+tc.host+"/v1/sites/shop/storage/people", nil)
			r.SetPathValue("sitename", "shop")
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, VisitorID: s.AliceID, VisitorAI: tc.ai}
			if tc.anonymous {
				c.VisitorID = ""
			}
			before := p.calls
			c.OnOwnAddress = !c.VisitorAI && s.A.Access.StorageOwnAddress(r, s.Sid)
			if got := h.StorageSvc().OwnerOnSite(r.Context(), c); got != tc.want {
				t.Fatalf("owner=%v want=%v", got, tc.want)
			}
			wantCalls := 0
			if tc.want {
				wantCalls = 1
			}
			if p.calls-before != wantCalls {
				t.Fatalf("provider calls=%d want=%d", p.calls-before, wantCalls)
			}
			if h.StorageDeps().Bindings != p {
				t.Fatal("request deps lost installed provider")
			}
		})
	}
}

func TestStorageBindingsNilPlatform(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.A.Data.SetStorageBindings(nil)
	s.Must(t, "PUT", "/resources/private", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/private/keys/key", `{"value":"only owner"}`, s.Key, 200)
	for _, who := range []string{"owner-site", "alice"} {
		owner := who == "owner-site"
		status := 403
		if owner {
			status = 200
		}
		s.Must(t, "GET", "/kv/private/keys/key", nil, s.Callers[who], status)
		me := s.A.At(t, "GET", s.Host, "/v1/sites/shop/me", nil, s.Callers[who])
		if me.Status != 200 || (me.JSON(t)["site_owner"] == true) != owner {
			t.Fatalf("%s me: %s", who, me.Body)
		}
		page := s.A.At(t, "GET", s.Host, "/", nil, s.Callers[who])
		guarded := page.Header.Get("X-Frame-Options") == "SAMEORIGIN" && strings.Contains(page.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'")
		if page.Status != 200 || guarded != owner {
			t.Fatalf("%s frame: %d %v", who, page.Status, page.Header)
		}
	}
}
