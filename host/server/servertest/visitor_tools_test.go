// The connector visitor tools (host/handler VisitorTools) admit a visitor
// through Access and read or write through Data: cross-module, so here.
package servertest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

func TestVisitorMCPNamedViewerPresetsAndRemoval(t *testing.T) {
	f := NewVisitorFixture(t)
	for _, preset := range []string{"records", "personal", "wall", "board", "private"} {
		rules := map[string]string{"records": RulesRecords, "personal": RulesPersonal, "wall": RulesWall, "board": RulesBoard, "private": string(model.OwnerOnlyRules)}[preset]
		f.Must(t, "PUT", "/resources/"+preset, StorageRules("sqlite", rules), f.Key, 201)
		f.Must(t, "POST", "/sqlite/"+preset+"/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, text TEXT)"}, f.Key, 200)
	}
	// Browser Alice's records must never appear in Bob's connector result.
	f.Must(t, "POST", "/sqlite/records/tables/entries/rows", map[string]any{"text": "alice-only"}, f.Callers["alice"], 200)
	other := f.Must(t, "POST", "/sqlite/board/tables/entries/rows", map[string]any{"text": "other-person"}, f.Callers["alice"], 200).JSON(t)["last_insert_id"]
	f.Named(t)
	f.Call(t, "visitor_read_site", map[string]any{}, "")
	all := f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host, "page": "index.html"}, "")
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), "alice-only") {
		t.Fatal("records disclosed another person's entry")
	}
	for _, preset := range []string{"records", "personal", "wall", "board"} {
		t.Run(preset, func(t *testing.T) {
			args := f.Args("sqlite", preset)
			args["table"] = "entries"
			args["value"] = map[string]any{"text": "mine", "visitor_id": f.Ids["alice"]}
			out := f.Call(t, "visitor_add_entry", args, "")
			id := out["visitor_data"].(map[string]any)["last_insert_id"]
			read := f.Args("sqlite", preset)
			read["table"] = "entries"
			got := f.Call(t, "visitor_read_site", read, "")
			raw, _ := json.Marshal(got)
			mine, _ := got["visitor_data"].(map[string]any)["entries"].(map[string]any)["mine"].([]any)
			hasMine := false
			for _, owned := range mine {
				if owned == true {
					hasMine = true
				}
			}
			if !hasMine {
				t.Fatalf("missing own marker: %s", raw)
			}
			args["id"] = id
			args["action"] = "edit"
			args["value"] = map[string]any{"text": "edited"}
			want := ""
			if preset == "records" || preset == "wall" {
				want = "add_only"
			}
			f.Call(t, "visitor_change_entry", args, want)
			args["action"] = "delete"
			delete(args, "value")
			want = ""
			if preset == "records" || preset == "board" {
				want = "add_only"
			}
			f.Call(t, "visitor_change_entry", args, want)
		})
	}
	args := f.Args("sqlite", "board")
	args["table"] = "entries"
	args["id"] = other
	args["action"] = "edit"
	args["value"] = map[string]any{"text": "hijacked"}
	f.Call(t, "visitor_change_entry", args, "not_found")
	args = f.Args("sqlite", "private")
	args["table"] = "entries"
	f.Call(t, "visitor_read_site", args, "forbidden")
	f.Call(t, "storage_list_resources", map[string]any{"site": "shop"}, "owner credential required")
	f.Call(t, "set_site_access", map[string]any{"site": "shop", "access": "anyone"}, "not found")
	// The /mcp audience is not a REST credential.
	res := f.A.Do(t, "GET", "/v1/me", nil, map[string]string{"Authorization": "Bearer " + f.Token})
	if res.Status != 401 {
		t.Fatalf("MCP token accepted by REST: %d", res.Status)
	}
	if _, err := f.A.Database.Exec(`DELETE FROM site_viewers WHERE site_id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, "site_private")
	f.Call(t, "visitor_add_entry", map[string]any{"address": "https://" + f.Host, "kind": "kv", "resource": "wall", "key": "x", "value": 1}, "site_private")
}

func TestVisitorMCPKVOwnerIsolationAuditAndCap(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesPersonal), f.Key, 201)
	args := f.Args("kv", "notes")
	args["key"] = "one"
	args["value"] = "keep"
	f.Call(t, "visitor_add_entry", args, "")
	f.Call(t, "visitor_add_entry", args, "key_exists")
	args["action"] = "edit"
	args["key"] = "absent"
	f.Call(t, "visitor_change_entry", args, "key_not_found")
	f.Must(t, "PUT", "/kv/notes/keys/alice", map[string]any{"value": "alice"}, f.Callers["alice"], 200)
	args["key"] = "alice"
	f.Call(t, "visitor_change_entry", args, "key_exists")
	args["action"] = "delete"
	delete(args, "value")
	f.Call(t, "visitor_change_entry", args, "key_not_found")
	var app string
	author := ReadStorageKVAuthor(t, f.A.Data, f.Sid, "notes", "one")
	if author != f.Ids["bob"] {
		t.Fatalf("author %q", author)
	}
	if err := f.A.Database.QueryRow(`SELECT actor_app FROM data_history WHERE site_id=$1 ORDER BY id DESC LIMIT 1`, f.Sid).Scan(&app); err != nil || app != "ChatGPT" {
		t.Fatalf("app %q: %v", app, err)
	}
	// Even a site owner's visitor tools have no owner or owner-on-site powers.
	client := f.A.RegisterClient(t, TestRedirect)
	ownerToken := f.A.Connect(t, f.Owner, client, TestRedirect)["access_token"].(string)
	owner := f
	owner.Token = ownerToken
	args = f.Args("kv", "notes")
	args["key"] = "one"
	owner.Call(t, "visitor_read_site", args, "key_not_found")
	got := owner.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, "")
	if !strings.Contains(got["note"].(string), "Use the owner tools") {
		t.Fatal("no owner guidance")
	}
	WithPasscodeLimits(t, func(l *config.Limits) { l.VisitorAIWritesPerDay = 2 }, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits)
	if _, err := f.A.Database.Exec(`DELETE FROM visitor_ai_daily WHERE site_id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		args = f.Args("kv", "notes")
		args["key"] = fmt.Sprint(i)
		args["value"] = i
		f.Call(t, "visitor_add_entry", args, "")
	}
	args["key"] = "capped"
	f.Call(t, "visitor_add_entry", args, "daily limit of 2")
	// A second app shares the same person's cap.
	client = f.A.RegisterClient(t, TestRedirect)
	key, undo, err := accountsrepo.IssueInternalKey(f.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	second := f
	second.Token = f.A.Connect(t, Person{Key: key}, client, TestRedirect)["access_token"].(string)
	second.Call(t, "visitor_add_entry", args, "daily limit of 2")
}

func TestVisitorMCPPasscodeAndBrowserLockout(t *testing.T) {
	f := NewVisitorFixture(t)
	if err := f.A.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeSite = config.Rate{Burst: 3, Every: time.Hour}
		l.RatePasscodeIP = config.Rate{Burst: 20, Every: time.Hour}
	}, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits, &f.A.Access.Passcode.Limits)
	r := f.A.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key)
	if r.Status != 200 {
		t.Fatalf("lock: %s", r.Body)
	}
	args := map[string]any{"address": "https://" + f.Host}
	f.Call(t, "visitor_read_site", args, "site_locked")
	args["passcode"] = "123456"
	f.Call(t, "visitor_read_site", args, "")
	// An explicitly submitted blank passcode spends a try, like the browser.
	args["passcode"] = ""
	f.Call(t, "visitor_read_site", args, "site_locked")
	args["passcode"] = "wrong"
	f.Call(t, "visitor_read_site", args, "site_locked")
	// Browser tries have their own site bucket.
	request := httptest.NewRequest("POST", "https://"+f.Host+"/__unlock", strings.NewReader(url.Values{"passcode": {"wrong"}, "next": {"/"}}.Encode()))
	request.Header.Set("Origin", "https://"+f.Host)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.A.Access.UnlockSite(rec, request)
	if strings.Contains(rec.Body.String(), "Too many tries") {
		t.Fatal("AI tries locked browser")
	}
	args["passcode"] = "wrong"
	f.Call(t, "visitor_read_site", args, "passcode_locked")
	args["passcode"] = "123456"
	f.Call(t, "visitor_read_site", args, "passcode_locked")
}

// The visitor tool bridge used to flatten every refusal into a plain error
// before it reached the MCP dispatcher, so AgentCat recorded all of these as
// the generic tool_error instead of their own code (SIM-359). Calling the
// bridge directly (bypassing the JSON-RPC envelope, which shows identical
// text either way) is the only way to see whether the code survived.
func TestVisitorToolBridgePreservesRefusalCode(t *testing.T) {
	f := NewVisitorFixture(t)
	if err := f.A.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeSite = config.Rate{Burst: 2, Every: time.Hour}
		l.RatePasscodeIP = config.Rate{Burst: 20, Every: time.Hour}
	}, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits, &f.A.Access.Passcode.Limits)
	call := func(args map[string]any) error {
		r := httptest.NewRequest("POST", "https://"+f.Host+"/mcp", nil)
		_, err := f.A.Conn.MCPConfig.VisitorTool(r, mcp.Caller{Owner: f.Ids["bob"], AppName: "Test"}, "visitor_read_site", args)
		return err
	}
	if code := mcp.RefusalCode(call(map[string]any{"address": "https://evil.invalid/"})); code != "site_not_found" {
		t.Errorf("unresolved address: code=%q", code)
	}
	// A proxied refusal (VisitorResponseError) that names its own REST code
	// -- here the offline gate, which runs ahead of the private/passcode
	// checks below -- is a different code path from the ones this file
	// builds by hand; it must carry a code through the bridge too.
	if err := sitesrepo.SetSiteOffline(context.Background(), f.A.Database, f.Sid, true); err != nil {
		t.Fatal(err)
	}
	if code := mcp.RefusalCode(call(map[string]any{"address": "https://" + f.Host})); code != "site_offline" {
		t.Errorf("offline site: code=%q", code)
	}
	if err := sitesrepo.SetSiteOffline(context.Background(), f.A.Database, f.Sid, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.A.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	if code := mcp.RefusalCode(call(map[string]any{"address": "https://" + f.Host})); code != "site_private" {
		t.Errorf("named-viewers-only site: code=%q", code)
	}
	if _, err := f.A.Database.Exec(`UPDATE sites SET access='anyone' WHERE id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	r := f.A.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key)
	if r.Status != 200 {
		t.Fatalf("lock: %s", r.Body)
	}
	if code := mcp.RefusalCode(call(map[string]any{"address": "https://" + f.Host})); code != "site_locked" {
		t.Errorf("no passcode submitted: code=%q", code)
	}
	if code := mcp.RefusalCode(call(map[string]any{"address": "https://" + f.Host, "passcode": "wrong"})); code != "site_locked" {
		t.Errorf("wrong passcode: code=%q", code)
	}
	err := call(map[string]any{"address": "https://" + f.Host, "passcode": "wrong"})
	if mcp.RefusalCode(err) != "passcode_locked" {
		t.Errorf("rate-limited passcode: code=%q", mcp.RefusalCode(err))
	}
	// The sanitization SetVisitorTools runs on the way out (VisitorPrivateData)
	// must not have eaten the one piece of the message that matters here: how
	// long the wait is.
	if err == nil || !strings.Contains(err.Error(), "minutes") {
		t.Errorf("passcode wait duration lost in sanitization: %v", err)
	}
}

func TestVisitorPassCannotCrossSitesOrBecomeOwner(t *testing.T) {
	f := NewVisitorFixture(t)
	if _, err := f.A.Database.Exec(`UPDATE users SET is_admin=true WHERE id=$1`, f.OwnerID); err != nil {
		t.Fatal(err)
	}
	f.A.Deploy(t, f.Owner, "other")
	otherID := f.A.SiteID(t, f.Owner, "other")
	pass, undo, err := accountsrepo.IssueInternalVisitorKey(accountsrepo.InternalVisitorPass{UserID: f.OwnerID, SiteID: f.Sid, Host: f.Host, AppName: "ChatGPT"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://"+f.Host+"/v1/sites/shop/storage/resources", nil)
	r.Header.Set("Origin", "https://"+f.Host)
	r.Header.Set(accesshttp.VisitorPassHeader, pass)
	r.SetPathValue("sitename", "shop")
	if _, ok := f.A.Access.InternalVisitorSession(r, otherID); ok {
		t.Fatal("pass accepted for other site")
	}
	rec := httptest.NewRecorder()
	f.A.Data.StorageRouteFor(f.A.Access.StorageCaller, f.A.Data.ListStorageResources).ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("owner storage accepted visitor: %d", rec.Code)
	}
	for _, path := range []string{"/v1/sites", "/v1/sites/shop/access", "/v1/me"} {
		result := f.A.At(t, "GET", PcSiteDomain, path, nil, map[string]string{"X-API-Key": pass})
		if result.Status != 401 {
			t.Fatalf("management accepted pass: %s %d", path, result.Status)
		}
	}
	r.SetPathValue("sitename", "other")
	rec = httptest.NewRecorder()
	f.A.Data.StorageRouteFor(f.A.Access.StorageCaller, f.A.Data.ListStorageResources).ServeHTTP(rec, r)
	if rec.Code == 200 {
		t.Fatal("cross-site name accepted")
	}
	r.SetPathValue("sitename", "shop")
	undo()
	if _, ok := f.A.Access.StrictVisitorSession(r, f.Sid); ok {
		t.Fatal("revoked pass accepted")
	}
}
