package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type storageBindingFixture struct {
	appstorage.NoBindings
	schema      ruleengine.BindingSchema
	schemaCalls int
}

func (p *storageBindingFixture) Schema() ruleengine.BindingSchema { p.schemaCalls++; return p.schema }
func (*storageBindingFixture) OwnerOnSite(context.Context, string, string) (bool, error) {
	return false, nil
}

func TestSetStorageBindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  ruleengine.Type
		bad  bool
	}{
		{"auth.id", ruleengine.TypeText, true}, {"row.x", ruleengine.TypeText, true},
		{"auth.teams", ruleengine.TypeBool, true}, {"auth.teams", ruleengine.TypeTextList, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &SiteHandler{}
			p := &storageBindingFixture{schema: ruleengine.BindingSchema{tc.name: {Type: tc.typ}}}
			var panicked bool
			func() { defer func() { panicked = recover() != nil }(); h.SetStorageBindings(p) }()
			if panicked != tc.bad {
				t.Fatalf("panicked=%t want %t", panicked, tc.bad)
			}
			if p.schemaCalls != 1 {
				t.Fatal("Schema called more than once")
			}
			if tc.bad {
				if h.storageBindings != nil {
					t.Fatal("invalid provider stored")
				}
				return
			}
			if h.storageDeps().Bindings != p || h.storageDeps().Bindings != p {
				t.Fatal("request services do not share provider")
			}
			if p.schemaCalls != 1 {
				t.Fatal("request read schema")
			}
		})
	}
	h := &SiteHandler{}
	h.SetStorageBindings(nil)
	if _, ok := h.storageDeps().Bindings.(appstorage.DefaultProvider); !ok {
		t.Fatal("nil did not select platform provider")
	}
}

type ownerBindingFixture struct {
	appstorage.NoBindings
	calls int
}

func (p *ownerBindingFixture) OwnerOnSite(context.Context, string, string) (bool, error) {
	p.calls++
	return true, nil
}

func TestStorageBindingRequestGuards(t *testing.T) {
	s := newPresetSite(t)
	h := s.a.sites
	h.ed = groupTestEdition()
	p := &ownerBindingFixture{}
	h.SetStorageBindings(p)
	preview := "https://" + s.host + "/__preview/1/" + h.signPreviewToken(s.sid, 1, time.Now().Add(time.Hour)) + "/"
	for _, tc := range []struct {
		name                string
		ai, anonymous       bool
		host, header, value string
		want                bool
	}{
		{name: "own host", host: s.host, want: true},
		{name: "visitor AI", host: s.host, ai: true},
		{name: "anonymous", host: s.host, anonymous: true},
		{name: "framed", host: s.host, header: "X-SH-Framed", value: "1"},
		{name: "preview", host: s.host, header: "Referer", value: preview},
		{name: "apex", host: pcSiteDomain},
		{name: "shared content", host: h.contentHost},
		{name: "unrelated host", host: "unrelated.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://"+tc.host+"/v1/sites/shop/storage/people", nil)
			r.SetPathValue("sitename", "shop")
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			c := storageCall{siteID: s.sid, ownerID: s.ownerID, visitorID: s.aliceID, visitorAI: tc.ai}
			if tc.anonymous {
				c.visitorID = ""
			}
			before := p.calls
			if got := h.storageSiteOwnerOnSite(r, c); got != tc.want {
				t.Fatalf("owner=%v want=%v", got, tc.want)
			}
			wantCalls := 0
			if tc.want {
				wantCalls = 1
			}
			if p.calls-before != wantCalls {
				t.Fatalf("provider calls=%d want=%d", p.calls-before, wantCalls)
			}
			if h.storageRequestDeps(r, c).Bindings != p {
				t.Fatal("request deps lost installed provider")
			}
		})
	}
}

func TestStorageBindingsNilPlatform(t *testing.T) {
	s := newPresetSite(t)
	s.a.sites.SetStorageBindings(nil)
	s.must(t, "PUT", "/resources/private", map[string]string{"kind": "kv", "preset": "private"}, s.key, 201)
	s.must(t, "PUT", "/kv/private/keys/key", `{"value":"only owner"}`, s.key, 200)
	for _, who := range []string{"owner-site", "alice"} {
		owner := who == "owner-site"
		status := 403
		if owner {
			status = 200
		}
		s.must(t, "GET", "/kv/private/keys/key", nil, s.callers[who], status)
		me := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, s.callers[who])
		if me.status != 200 || (me.json(t)["site_owner"] == true) != owner {
			t.Fatalf("%s me: %s", who, me.body)
		}
		page := s.a.at(t, "GET", s.host, "/", nil, s.callers[who])
		guarded := page.header.Get("X-Frame-Options") == "SAMEORIGIN" && strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'self'")
		if page.status != 200 || guarded != owner {
			t.Fatalf("%s frame: %d %v", who, page.status, page.header)
		}
	}
}
