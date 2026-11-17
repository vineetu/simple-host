package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

func TestLegacyStorageCallLoggedOncePerSite(t *testing.T) {
	f := newVisitorFixture(t)
	if _, err := f.a.database.Exec(`UPDATE sites SET legacy_data=true WHERE id=$1`, f.sid); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	for i := 0; i < 2; i++ {
		f.call(t, "visitor_read_site", f.args("collection", "guestbook"), "")
		if n := strings.Count(logs.String(), "legacy_storage_call site="+f.sid); n != 1 {
			t.Fatalf("after visitor call %d logged %d times: %s", i+1, n, logs.String())
		}
	}
	for _, path := range []string{"state", "collections", "data", "state"} {
		r := f.a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/"+path, nil, f.key)
		if r.status != 200 {
			t.Fatalf("GET %s: %d %s", path, r.status, r.body)
		}
	}
	if n := strings.Count(logs.String(), "legacy_storage_call site="+f.sid); n != 1 {
		t.Fatalf("logged %d times: %s", n, logs.String())
	}
}

func TestLegacyStorageCallOwnerClearEmptyCollection(t *testing.T) {
	a := newPrivateApp(t)
	owner := a.newPerson(t, "notice-owner")
	a.deploy(t, owner, "shop")
	siteID := a.siteID(t, owner, "shop")
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	for i := 0; i < 2; i++ {
		r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/collections/guestbook", map[string]string{"confirm": "guestbook"}, map[string]string{"X-API-Key": owner.key})
		if r.status != 200 || r.json(t)["deleted"] != float64(0) {
			t.Fatalf("clear empty collection: %d %s", r.status, r.body)
		}
		if n := strings.Count(logs.String(), "legacy_storage_call site="+siteID); n != 1 {
			t.Fatalf("after clear %d logged %d times: %s", i+1, n, logs.String())
		}
	}
}

func TestPersonalRecordsInVisitorAccountExport(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "personal-notice", map[string]any{"kind": "personal"})
	r := s.visitor(t, "PUT", "/v1/sites/shop/data/personal-notice", map[string]any{"private_note": "visitor-only"})
	if r.status != 200 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	r = s.a.at(t, "GET", pcSiteDomain, "/v1/me/export.zip", nil, map[string]string{"X-API-Key": s.vic.key})
	if r.status != 200 {
		t.Fatalf("account export: %d %s", r.status, r.body)
	}
	var visitor struct {
		Submitted []struct {
			List string         `json:"list"`
			Item map[string]any `json:"item"`
		} `json:"submitted"`
	}
	if err := json.Unmarshal([]byte(fileUnder(t, zipFiles(t, r.body), "/visitor.json")), &visitor); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range visitor.Submitted {
		if item.List == "personal-notice" && item.Item["private_note"] == "visitor-only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Personal record absent from account export: %+v", visitor)
	}
}
