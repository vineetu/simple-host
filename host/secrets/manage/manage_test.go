package manage

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

const value = "not-a-real-key-0123456789-abcdefghijklmnopqrstuv"

// An owner saves, replaces, lists and deletes a key; the value is never
// answered back, is stored only sealed under a 0600 master key file made on
// the first save, and opens again only through Open, bound to its site and
// name.
func TestSecretIsWriteOnly(t *testing.T) {
	a := newApp(t)
	ann := rt.NewPerson(t, a.db, "secrets-owner")
	siteID := deploy(t, a, ann, "shop")
	if _, err := os.Stat(a.keyFile); !os.IsNotExist(err) {
		t.Fatalf("key file before the first save: %v", err)
	}
	r := a.put(t, ann, "shop", "resend", value, "API.Resend.com", "owner")
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"destination":"api.resend.com"`) {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	noValue(t, "save answer", r.Body, value)
	if fi, err := os.Stat(a.keyFile); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	var stored []byte
	if err := a.db.QueryRow(`SELECT ciphertext FROM site_secrets WHERE site_id = $1 AND name = 'resend'`, siteID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	noValue(t, "stored row", stored, value)

	// Replace: one row, the new settings.
	if r := a.put(t, ann, "shop", "resend", value+"-2", "api.resend.com", "signed-in"); r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"who":"signed-in"`) {
		t.Fatalf("replace: %d %s", r.Status, r.Body)
	}
	r = a.call(t, ann, "GET", "/v1/sites/shop/secrets", nil)
	if r.Status != http.StatusOK || strings.Count(string(r.Body), `"name"`) != 1 || !strings.Contains(string(r.Body), `"use_count":0`) {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	noValue(t, "list", r.Body, value)
	got, opened, err := a.store.Open(context.Background(), siteID, "resend")
	if err != nil || got != value+"-2" || opened.Destination != "api.resend.com" || opened.AttachName != "Authorization" {
		t.Fatalf("open: %q %+v %v", got, opened, err)
	}
	// A sealed value moved to another name does not open.
	if _, err := a.db.Exec(`UPDATE site_secrets SET name = 'moved' WHERE site_id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.Open(context.Background(), siteID, "moved"); err == nil {
		t.Fatal("a value moved to another name opened")
	}
	if r := a.call(t, ann, "DELETE", "/v1/sites/shop/secrets/moved", nil); r.Status != http.StatusOK || strings.Contains(string(r.Body), `"name"`) {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.call(t, ann, "DELETE", "/v1/sites/shop/secrets/moved", nil); r.Status != http.StatusNotFound || r.Code() != "secret_not_found" {
		t.Fatalf("delete again: %d %s", r.Status, r.Body)
	}
	noValue(t, "logs", a.logs.Bytes(), value)
}

// Only the site's owner reaches its secrets; a bad name, value, destination
// a missing who or a bad attach is refused without echoing the value; a
// site holds at most 20.
func TestSecretRefusals(t *testing.T) {
	a := newApp(t)
	ann := rt.NewPerson(t, a.db, "secrets-ann")
	bob := rt.NewPerson(t, a.db, "secrets-bob")
	deploy(t, a, ann, "shop")
	for _, c := range []struct{ name, value, dest, who, code string }{
		{"Resend", value, "api.resend.com", "owner", "invalid_secret_name"},
		{"resend", "", "api.resend.com", "owner", "invalid_secret_value"},
		{"resend", strings.Repeat("x", 8193), "api.resend.com", "owner", "invalid_secret_value"},
		{"resend", value, "https://api.resend.com", "owner", "invalid_destination"},
		{"resend", value, "api.resend.com/emails", "owner", "invalid_destination"},
		{"resend", value, "api.resend.com:443", "owner", "invalid_destination"},
		{"resend", value, "10.0.0.1", "owner", "invalid_destination"},
		{"resend", value, "localhost", "owner", "invalid_destination"},
		{"resend", value, "db.internal", "owner", "invalid_destination"},
		{"resend", value, "api.resend.com", "", "invalid_who"},
		{"resend", value, "api.resend.com", "everyone", "invalid_who"},
		{"resend", "line\r\nX-Evil: 1", "api.resend.com", "owner", "invalid_secret_value"},
	} {
		r := a.put(t, ann, "shop", c.name, c.value, c.dest, c.who)
		if r.Status != http.StatusBadRequest || r.Code() != c.code {
			t.Fatalf("%+v: %d %s", c.code, r.Status, r.Body)
		}
		noValue(t, "refusal", r.Body, value)
	}
	for _, attach := range []map[string]string{
		{"attach": "", "attach_name": "Authorization"},
		{"attach": "header", "attach_name": ""},
		{"attach": "header", "attach_name": "Cookie"},
		{"attach": "header", "attach_name": "Host"},
		{"attach": "query", "attach_name": "key&x=1"},
		{"attach": "header", "attach_name": "Authorization", "attach_prefix": "Bearer\r\n"},
	} {
		body := map[string]string{"value": value, "destination": "api.resend.com", "who": "owner"}
		for k, v := range attach {
			body[k] = v
		}
		if r := a.call(t, ann, "PUT", "/v1/sites/shop/secrets/resend", body); r.Status != http.StatusBadRequest || !strings.HasPrefix(r.Code(), "invalid_attach") {
			t.Fatalf("attach %v: %d %s", attach, r.Status, r.Body)
		}
	}
	if r := a.put(t, bob, "shop", "resend", value, "api.resend.com", "owner"); r.Status != http.StatusNotFound || r.Code() != "site_not_found" {
		t.Fatalf("another person's save: %d %s", r.Status, r.Body)
	}
	for i := range 20 {
		if r := a.put(t, ann, "shop", "k"+strconv.Itoa(i), value, "api.resend.com", "anyone"); r.Status != http.StatusOK {
			t.Fatalf("save %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := a.put(t, ann, "shop", "k20", value, "api.resend.com", "anyone"); r.Status != http.StatusConflict || r.Code() != "too_many_secrets" {
		t.Fatalf("21st: %d %s", r.Status, r.Body)
	}
	if r := a.put(t, ann, "shop", "k0", value, "api.resend.com", "owner"); r.Status != http.StatusOK {
		t.Fatalf("replace at the limit: %d %s", r.Status, r.Body)
	}
	noValue(t, "logs", a.logs.Bytes(), value)
}

// A row edited in the database does not open: where and how the key is sent
// are sealed with it, and a damaged nonce is an error, not a crash.
func TestEditedRowDoesNotOpen(t *testing.T) {
	a := newApp(t)
	ann := rt.NewPerson(t, a.db, "secrets-edit")
	siteID := deploy(t, a, ann, "shop")
	for _, edit := range []string{
		`UPDATE site_secrets SET destination = 'evil.example.net' WHERE site_id = $1`,
		`UPDATE site_secrets SET attach = 'query', attach_name = 'key' WHERE site_id = $1`,
		`UPDATE site_secrets SET attach_prefix = '' WHERE site_id = $1`,
		`UPDATE site_secrets SET nonce = '\x00' WHERE site_id = $1`,
	} {
		if r := a.put(t, ann, "shop", "resend", value, "api.resend.com", "owner"); r.Status != http.StatusOK {
			t.Fatalf("save: %d %s", r.Status, r.Body)
		}
		if _, err := a.db.Exec(edit, siteID); err != nil {
			t.Fatal(err)
		}
		if got, _, err := a.store.Open(context.Background(), siteID, "resend"); err == nil || got != "" {
			t.Errorf("%s: opened", edit)
		}
	}
}

// A destination that is this install's own host is refused, and a site the
// operator took down refuses changes to its keys but still lists them.
func TestOwnHostsAndHeldSites(t *testing.T) {
	a := newApp(t)
	ann := rt.NewPerson(t, a.db, "secrets-held")
	siteID := deploy(t, a, ann, "shop")
	for _, dest := range []string{ownDomain, "www." + ownDomain, "sites." + ownDomain, "cname." + ownDomain, "shop." + ann.Handle + "." + ownDomain, "x.events-" + ownDomain} {
		if r := a.put(t, ann, "shop", "self", value, dest, "owner"); r.Status != http.StatusBadRequest || r.Code() != "invalid_destination" {
			t.Errorf("%s: %d %s", dest, r.Status, r.Body)
		}
	}
	if r := a.put(t, ann, "shop", "resend", value, "api.resend.com", "owner"); r.Status != http.StatusOK {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	if _, err := a.db.Exec(`UPDATE sites SET suspended_at = now() WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if r := a.put(t, ann, "shop", "resend", value, "api.resend.com", "owner"); r.Status != http.StatusForbidden || r.Code() != "site_suspended" {
		t.Errorf("save on a held site: %d %s", r.Status, r.Body)
	}
	if r := a.call(t, ann, "DELETE", "/v1/sites/shop/secrets/resend", nil); r.Status != http.StatusForbidden || r.Code() != "site_suspended" {
		t.Errorf("delete on a held site: %d %s", r.Status, r.Body)
	}
	if r := a.call(t, ann, "GET", "/v1/sites/shop/secrets", nil); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "resend") {
		t.Errorf("list on a held site: %d %s", r.Status, r.Body)
	}
}

// The master file: never made again once keys are saved (that would split
// them across two keys), and refused when other users can read it.
func TestMasterFileRules(t *testing.T) {
	a := newApp(t)
	ann := rt.NewPerson(t, a.db, "secrets-master")
	deploy(t, a, ann, "shop")
	if r := a.put(t, ann, "shop", "resend", value, "api.resend.com", "owner"); r.Status != http.StatusOK {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	if err := os.Chmod(a.keyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := a.put(t, ann, "shop", "other", value, "api.resend.com", "owner"); r.Status != http.StatusInternalServerError || !strings.Contains(a.logs.String(), "readable by other users") {
		t.Errorf("readable master file: %d %s", r.Status, r.Body)
	}
	if err := os.Remove(a.keyFile); err != nil {
		t.Fatal(err)
	}
	if r := a.put(t, ann, "shop", "other", value, "api.resend.com", "owner"); r.Status != http.StatusInternalServerError || !strings.Contains(a.logs.String(), a.keyFile+" is missing while saved secrets exist") {
		t.Errorf("missing master file: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.keyFile); !os.IsNotExist(err) {
		t.Errorf("a new master file was made while secrets exist: %v", err)
	}
	if path, present := a.store.KeyFile(); path != a.keyFile || present {
		t.Errorf("start-up line: %s %v", path, present)
	}
	noValue(t, "logs", a.logs.Bytes(), value)
}
