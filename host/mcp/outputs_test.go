package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEveryToolDeclaresAWellFormedOutputSchema(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Name] = true
		if tool.OutputSchema == nil {
			t.Errorf("%s has no outputSchema", tool.Name)
			continue
		}
		if err := CheckOutputSchema(tool.OutputSchema); err != nil {
			t.Errorf("%s outputSchema: %v", tool.Name, err)
		}
	}
	for name := range outputSchemasWithLimits(DefaultLimits()) {
		if !names[name] {
			t.Errorf("outputSchemas has %q, which is not a tool", name)
		}
	}
}

// The checker must actually refuse bad values and bad schemas, or every test
// built on it passes vacuously.
func TestSchemaCheckerRefusesWhatItShould(t *testing.T) {
	schema := outObject(map[string]any{
		"name":  outString("n"),
		"count": outInteger("c"),
		"kind":  outEnum("k", "a", "b"),
		"maybe": map[string]any{"type": []string{"string", "null"}},
		"list":  outArray("l", outObject(map[string]any{"x": outBool("x")}, "x")),
		"free":  anyJSON("f"),
	}, "name", "count")
	good := map[string]any{"name": "n", "count": 2, "kind": "a", "maybe": nil, "list": []any{map[string]any{"x": true}}, "free": []any{1, "two"}}
	if err := ValidateOutput(schema, good, nil); err != nil {
		t.Fatalf("valid value refused: %v", err)
	}
	for label, bad := range map[string]map[string]any{
		"missing required": {"name": "n"},
		"extra property":   {"name": "n", "count": 1, "other": 1},
		"wrong type":       {"name": 3, "count": 1},
		"not in enum":      {"name": "n", "count": 1, "kind": "c"},
		"bad array item":   {"name": "n", "count": 1, "list": []any{map[string]any{}}},
	} {
		if err := ValidateOutput(schema, bad, nil); err == nil {
			t.Errorf("%s: accepted %v", label, bad)
		}
	}
	for label, bad := range map[string]any{
		"root not object":     map[string]any{"type": "array", "items": map[string]any{}},
		"unknown keyword":     outObject(map[string]any{"a": map[string]any{"type": "string", "format": "uri"}}),
		"required undeclared": outObject(map[string]any{"a": outString("a")}, "b"),
		"unknown type":        outObject(map[string]any{"a": map[string]any{"type": "text"}}),
		"array without items": outObject(map[string]any{"a": map[string]any{"type": "array"}}),
	} {
		if err := CheckOutputSchema(bad); err == nil {
			t.Errorf("%s: schema accepted", label)
		}
	}
}

// Every tool returns structuredContent that its outputSchema accepts. The end-to-end twin against the real
// application is TestOutputSchemasMatchRealResults in host/handler.
func TestEveryToolResultMatchesItsOutputSchema(t *testing.T) {
	site := func(name string, version int, vis, domain, status string) string {
		m := map[string]any{"id": "s-" + name, "user_id": "u-1", "name": name, "active_version": version,
			"site_url": "https://sites.simple-host.app/ann/" + name + "/", "visibility": vis,
			"views": map[string]any{"people_7_days": 2, "people_30_days": 9}}
		m["summary"] = map[string]any{"title": "Page title", "description": "Page description", "headings": "First heading", "text": "Page text"}
		if domain != "" {
			m["custom_domain"], m["domain_status"] = domain, status
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	fixed := func(status int, body string) func() (int, string) {
		return func() (int, string) { return status, body }
	}
	// owner is the site owner's own visits; the tool must leave it out of the
	// structured split (the schema has no such key) and say it in text.
	split := `{"person":{"views":5,"visitors":2},"bot":{"views":9,"visitors":3},"infra":{"views":1,"visitors":1},"owner":{"views":4,"visitors":1},"unknown":{"views":0,"visitors":0}}`
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/me/bio":                   fixed(200, `{"bio":"My projects","max_length":280}`),
		"PUT /v1/sites/blog/showcase":      fixed(200, `{"site":"blog","pinned":true,"order":2}`),
		"PUT /v1/me/home":                  fixed(200, `{"site":null}`),
		"PUT /v1/sites/blog/keep-versions": fixed(200, `{"name":"blog","keep_versions":0,"effective_keep_versions":4,"active_version":2,"removed_versions":[]}`),
		"GET /v1/me": fixed(200, `{"file_usage":{"used_bytes":100,"limit_mb":1024,"message":"0.0 MB of 1 GB used","tips":"Shrink photos"},"can_set_keep_versions":true,"keep_versions":4,"id":"u-1","username":"a@example.com","handle":"ann","display_name":"Ann",`+
			`"address":{"state":"waiting","address":"https://<site>.ann.simple-host.app/","ready_in_hours":3,"note":"Your sites are at ann.simple-host.app/<site>/ until ..."},"teams":[{"id":"t-1","handle":"studio"}],`+
			`"team_invitations":[{"id":"i-1","team":"crew","invited_by":"cal","created_at":"2026-11-19T00:00:00Z","expires_at":"2026-12-03T00:00:00Z"}]}`),
		"GET /v1/sites": fixed(200, "["+site("blog", 2, "public", "rsvp.example.com", "active")+","+
			strings.TrimSuffix(site("draft", 0, "unlisted", "", ""), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ann.simple-host.app/<site>/ until ..."}}`+","+site("pend", 1, "public", "pend.example.com", "pending")+","+
			strings.Replace(site("broken", 1, "unlisted", "broken.example.com", "error"), "{", `{"offline":true,"owner":"studio","access_role":"member",`, 1)+"]"),
		"POST /v1/teams":                          fixed(201, `{"id":"t-1","handle":"studio","members":1,"sites":0,"created_at":"2026-11-19T00:00:00Z"}`),
		"GET /v1/teams":                           fixed(200, `{"teams":[{"id":"t-1","handle":"studio","members":2,"sites":1,"created_at":"2026-11-19T00:00:00Z"}],"invitations":[{"id":"i-1","team":"crew","invited_by":"cal","created_at":"2026-11-19T00:00:00Z","expires_at":"2026-12-03T00:00:00Z"}]}`),
		"GET /v1/teams/studio/members":            fixed(200, `{"team":{"id":"t-1","handle":"studio","members":2,"sites":1,"created_at":"2026-11-19T00:00:00Z"},"members":[{"id":"u-1","handle":"ann","display_name":"Ann","added_at":"2026-11-19T00:00:00Z"},{"id":"u-2","handle":"ben","added_by":"ann","added_at":"2026-11-19T00:00:00Z"}],"invitations":[{"id":"i-2","team":"studio","addressed_to":"cal","invited_by":"ann","created_at":"2026-11-19T00:00:00Z","expires_at":"2026-12-03T00:00:00Z"}]}`),
		"POST /v1/teams/studio/members":           fixed(202, `{"invited":true,"expires_in_days":14,"note":"Nothing is sent."}`),
		"DELETE /v1/teams/studio/invitations/i-2": fixed(200, `{"cancelled":"i-2"}`),
		"POST /v1/me/team-invitations/i-1":        fixed(200, `{"team":"crew","answer":"accept"}`),
		"DELETE /v1/teams/studio/members/cal":     fixed(200, `{"removed":"cal"}`),
		"DELETE /v1/teams/studio":                 fixed(200, `{"deleted":"studio"}`),
		"GET /v1/sites/blog/versions/2/files":     fixed(200, `{"version_number":2,"files":[{"path":"index.html","size":11,"content_type":"text/html; charset=utf-8","content":"<h1>hi</h1>"},{"path":"big.txt","error":"file not found","status":404}]}`),
		"GET /v1/sites/blog/versions/live/files":  fixed(200, `{"version_number":2,"files":[{"path":"index.html","size":11,"content_type":"text/html; charset=utf-8","content":"<h1>hi</h1>"}]}`),
		"GET /v1/sites/blog/versions/1/files":     fixed(200, `{"version_number":1,"files":[{"path":"logo.png","size":6,"content_type":"image/png","binary":true}]}`),
		"POST /v1/sites/fresh/files":              fixed(201, strings.TrimSuffix(site("fresh", 1, "unlisted", "", ""), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"PUT /v1/sites/blog/files":                fixed(200, strings.TrimSuffix(site("blog", 3, "public", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"GET /v1/sites/blog/versions": fixed(200, `[{"version_number":1,"created_at":"2026-09-01T00:00:00Z","is_active":false,"status":"active"},`+
			`{"version_number":2,"created_at":"2026-09-02T00:00:00Z","is_active":true,"status":"active"},`+
			`{"version_number":3,"created_at":"2026-09-03T00:00:00Z","is_active":false,"status":"ready"}]`),
		"PUT /v1/sites/pend/files": fixed(200, strings.Replace(site("pend", 1, "public", "pend.example.com", "pending"), "{",
			`{"unpublished_version":2,"preview_url":"https://pend.ann.simple-host.app/__preview/2/abc.def/",`, 1)),
		"POST /v1/sites/blog/versions/3/preview-link":   fixed(200, `{"site":"blog","version":3,"live":false,"url":"https://blog.ann.simple-host.app/__preview/3/abc.def/","expires_at":"2026-09-27T11:00:00Z","expires_in":3600}`),
		"PUT /v1/sites/blog/active-version":             fixed(200, strings.TrimSuffix(site("blog", 1, "public", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"DELETE /v1/sites/blog":                         fixed(204, ""),
		"DELETE /v1/sites/blog/versions/3":              fixed(200, `{"site":"blog","discarded_version":3,"active_version":2}`),
		"GET /v1/me/deleted-sites":                      fixed(200, `{"sites":[{"name":"old","deleted_at":"2026-09-20T00:00:00Z","purge_at":"2026-09-27T00:00:00Z"}],"retention_days":7}`),
		"POST /v1/sites/old/restore":                    fixed(200, strings.TrimSuffix(site("old", 2, "unlisted", "old.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"PATCH /v1/sites/blog":                          fixed(200, strings.TrimSuffix(site("journal", 2, "unlisted", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"failing","note":"Your sites are at ..."}}`),
		"PUT /v1/sites/blog/visibility":                 fixed(200, `{"visibility":"unlisted"}`),
		"PATCH /v1/sites/draft":                         fixed(200, strings.Replace(site("draft", 0, "unlisted", "", ""), "{", `{"offline":true,`, 1)),
		"PUT /v1/sites/blog/lock":                       fixed(200, `{"site":"blog","passcode_protected":true,"passcode":"482915","passcode_set_at":"2026-09-29T00:00:00Z","note":"Visitors now see a protected page."}`),
		"GET /v1/sites/blog/lock":                       fixed(200, `{"site":"blog","passcode_protected":true,"passcode":"482915","passcode_set_at":"2026-09-29T00:00:00Z","note":"Set it again to change it."}`),
		"DELETE /v1/sites/blog/lock":                    fixed(200, `{"site":"blog","passcode_protected":false,"note":"Open to everyone again."}`),
		"POST /v1/sites/blog/lock/sign-out-everyone":    fixed(200, `{"site":"blog","passcode_protected":true,"note":"Everyone must enter it again."}`),
		"GET /v1/sites/blog/access":                     fixed(200, `{"site":"blog","access":"specific","viewers":[{"email":"mom@example.com","added_at":"2026-10-10T00:00:00Z","signed_in_at":"2026-10-11T00:00:00Z"}],"viewer_limit":50,"passcode_protected":false}`),
		"PUT /v1/sites/blog/access":                     fixed(200, `{"site":"blog","access":"specific","viewers":[{"email":"mom@example.com","added_at":"2026-10-10T00:00:00Z","signed_in_at":"2026-10-11T00:00:00Z"}],"viewer_limit":50,"passcode_protected":false,"note":"Only named viewers."}`),
		"POST /v1/sites/blog/viewers":                   fixed(200, `{"site":"blog","access":"specific","viewers":[{"email":"mom@example.com","added_at":"2026-10-10T00:00:00Z","signed_in_at":"2026-10-11T00:00:00Z"}],"viewer_limit":50,"passcode_protected":false,"added":["mom@example.com"],"already_listed":["x@example.com"],"note":"Only named viewers."}`),
		"DELETE /v1/sites/blog/viewers/dad@example.com": fixed(200, `{"site":"blog","access":"specific","viewers":[{"email":"mom@example.com","added_at":"2026-10-10T00:00:00Z","signed_in_at":"2026-10-11T00:00:00Z"}],"viewer_limit":50,"passcode_protected":false,"removed":"dad@example.com","note":"dad can no longer open it."}`),
		"GET /v1/sites/blog/storage/visitors":           fixed(200, `{"visitors":[{"visitor_id":"1b4e28ba-2fa1-11d2-883f-0016d3cca427","email":"mom@example.com","found":true},{"visitor_id":"2b4e28ba-2fa1-11d2-883f-0016d3cca427","found":false}]}`),
		"PUT /v1/sites/blog/keep":                       fixed(200, `{"name":"blog","keep":true}`),
		"POST /v1/sites/pend/domain": fixed(200, `{"domain":"pend.example.com","status":"pending","took_over_from":"x/y","certificate_status":"pending","previous_domain":"pend.simple-host.app",`+
			`"partner_domain":"www.pend.example.com","partner_status":"pending","dns_partner":{"type":"CNAME","host":"www.pend.example.com","value":"sites.simple-host.app"},`+
			`"dns":{"type":"CNAME","host":"pend.example.com","value":"sites.simple-host.app"},"dns_txt":{"type":"TXT","host":"_simple-host.pend.example.com","value":"sh-0123456789abcdef0123456789abcdef"}}`),
		"GET /v1/sites/blog/domain": fixed(200, `{"domain":"rsvp.example.com","status":"active","verified_at":"2026-09-01T00:00:00Z","dns":{"type":"A","host":"rsvp.example.com","value":"192.0.2.1"}}`),
		"GET /v1/sites/pend/domain": fixed(200, `{"domain":"pend.example.com","status":"pending","bound_at":"2026-09-01T00:00:00Z","expires_at":"2026-09-02T00:00:00Z","certificate_status":"issuing","previous_domain":"pend.simple-host.app",`+
			`"dns":{"type":"CNAME","host":"pend.example.com","value":"sites.simple-host.app"},"dns_txt":{"type":"TXT","host":"_simple-host.pend.example.com","value":"sh-0123456789abcdef0123456789abcdef"}}`),
		"DELETE /v1/sites/blog/domain": fixed(200, site("blog", 2, "public", "", "")),
		"POST /v1/me/address-families": fixed(201, `{"id":"f-1","family":"*.trips.example.com","suffix":"trips.example.com","site_prefix":"","rank":0,"canonical":true,"status":"pending","live":false,`+
			`"bound_at":"2026-09-01T00:00:00Z","expires_at":"2026-09-02T00:00:00Z","dns":{"type":"CNAME","host":"*.trips.example.com","value":"cname.simple-host.app"},`+
			`"dns_txt":{"type":"TXT","host":"_simple-host.trips.example.com","value":"sh-0123"},"certificate":{"mode":"wildcard","status":"waiting_for_operator","note":"the operator sets it up"},"example_url":"https://blog.trips.example.com/"}`),
		"GET /v1/me/address-families/trips.example.com": fixed(200, `{"id":"f-1","family":"*.trips.example.com","suffix":"trips.example.com","site_prefix":"voucher-","rank":0,"canonical":false,"status":"active","live":true,`+
			`"bound_at":"2026-09-01T00:00:00Z","verified_at":"2026-09-01T01:00:00Z","dns":{"type":"CNAME","host":"*.trips.example.com","value":"cname.simple-host.app"},"last_error":"none",`+
			`"certificate":{"mode":"wildcard","status":"live"},"example_url":"https://blog.trips.example.com/"}`),
		"DELETE /v1/me/address-families/trips.example.com":        fixed(204, ""),
		"POST /v1/sites/blog/export-link":                         fixed(200, `{"site":"blog","url":"https://simple-host.app/v1/export?token=abc.def","expires_at":"2026-09-27T10:10:00Z","expires_in":600}`),
		"GET /v1/sites/blog/analytics":                            fixed(200, `{"range_days":7,"totals":`+split+`,"daily":[],"last_24h":`+split+`,"hourly":[],"classified_from":"2026-09-01"}`),
		"GET /v1/sites/blog/analytics/top":                        fixed(200, `{"range_days":7,"pages":[{"path":"/","views":3}],"referrers":[{"domain":"news.ycombinator.com","views":2}]}`),
		"GET /v1/sites/blog/storage/resources":                    fixed(200, `{"resources":[]}`),
		"GET /v1/sites/blog/storage/usage":                        fixed(200, `{"used_bytes":0,"limit_bytes":1000000,"remaining_bytes":1000000,"breakdown":{"kv_bytes":0,"sqlite_bytes":0,"files_bytes":0}}`),
		"PUT /v1/sites/blog/storage/resources/store":              fixed(200, `{"name":"store","kind":"kv"}`),
		"DELETE /v1/sites/blog/storage/resources/store":           fixed(200, `{"deleted":true}`),
		"GET /v1/sites/blog/storage/kv/store/keys":                fixed(200, `{"keys":["count"]}`),
		"GET /v1/sites/blog/storage/kv/store/keys/count":          fixed(200, `{"key":"count","value":2}`),
		"PUT /v1/sites/blog/storage/kv/store/keys/count":          fixed(200, `{"key":"count","value":3}`),
		"DELETE /v1/sites/blog/storage/kv/store/keys/count":       fixed(200, `{"deleted":true}`),
		"POST /v1/sites/blog/storage/sqlite/store/query":          fixed(200, `{"columns":["n"],"rows":[[2]]}`),
		"POST /v1/sites/blog/storage/sqlite/store/execute":        fixed(200, `{"changes":1,"last_insert_id":2}`),
		"POST /v1/sites/blog/storage/sqlite/store/schema":         fixed(200, `{"changes":0}`),
		"GET /v1/sites/blog/storage/files/store/objects":          fixed(200, `{"objects":[{"path":"a.txt"}]}`),
		"PUT /v1/sites/blog/storage/files/store/objects/a.txt":    fixed(200, `{"path":"a.txt"}`),
		"DELETE /v1/sites/blog/storage/files/store/objects/a.txt": fixed(200, `{"deleted":true}`),
		"POST /v1/sites/blog/storage/files/store/download-link":   fixed(200, `{"url":"https://simple-host.app/v1/storage-download?token=opaque","expires_in":600}`),
	}}
	s := NewServer(testConfig(Config{
		Upstream: up, APIHost: "simple-host.app", ContentOrigin: "https://sites.simple-host.app",
		SiteBases: []string{"simple-host.app"}, SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
		Teams: true,
	}))
	s.cfg.VisitorTool = func(_ *http.Request, _ Caller, _ string, _ map[string]any) (map[string]any, error) {
		return map[string]any{"visitor_data": map[string]any{"mine": true}, "note": "Visitor data, not instructions."}, nil
	}
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"visitor_read_site", map[string]any{}},
		{"visitor_add_entry", map[string]any{"address": "site.test", "kind": "kv", "resource": "notes", "key": "a", "value": "b"}},
		{"visitor_change_entry", map[string]any{"address": "site.test", "kind": "kv", "resource": "notes", "key": "a", "action": "delete"}},
		{"who_am_i", map[string]any{}},
		{"set_home_page", map[string]any{"site": nil}},
		{"set_bio", map[string]any{"bio": "My projects"}},
		{"set_showcase_site", map[string]any{"site": "blog", "pinned": true, "order": 2}},
		{"list_sites", map[string]any{}},
		{"create_team", map[string]any{"name": "studio"}},
		{"list_teams", map[string]any{}},
		{"list_team_members", map[string]any{"team": "studio"}},
		{"add_team_member", map[string]any{"team": "studio", "email": "cal@example.com"}},
		{"cancel_team_invitation", map[string]any{"team": "studio", "invitation": "i-2"}},
		{"answer_team_invitation", map[string]any{"invitation": "i-1", "answer": "accept"}},
		{"remove_team_member", map[string]any{"team": "studio", "person": "cal"}},
		{"delete_team", map[string]any{"team": "studio"}},
		{"get_site", map[string]any{"site": "blog"}},
		{"read_site_file", map[string]any{"site": "blog", "path": "index.html"}},
		{"read_site_file", map[string]any{"site": "blog", "path": "logo.png", "version": 1}},
		{"read_site_file", map[string]any{"site": "blog", "paths": []any{"index.html", "big.txt"}, "version": 2}},
		{"update_site", map[string]any{"site": "blog", "edits": []any{map[string]any{"path": "index.html", "find": "hi", "replace": "hello"}}}},
		{"create_site", map[string]any{"site": "fresh", "files": map[string]any{"index.html": "x"}}},
		{"create_upload_link", map[string]any{"site": "blog"}},
		{"set_keep_versions", map[string]any{"site": "blog", "keep_versions": 0}},
		{"list_versions", map[string]any{"site": "blog"}},
		{"update_site", map[string]any{"site": "pend", "files": map[string]any{"index.html": "x"}, "publish": false}},
		{"preview_version", map[string]any{"site": "blog", "version": 3}},
		{"discard_version", map[string]any{"site": "blog", "version": 3}},
		{"rollback_site", map[string]any{"site": "blog", "version": 1}},
		{"delete_site", map[string]any{"site": "blog", "confirm_name": "blog"}},
		{"rename_site", map[string]any{"site": "blog", "new_name": "journal"}},
		{"list_deleted_sites", map[string]any{}},
		{"restore_site", map[string]any{"site": "old"}},
		{"set_visibility", map[string]any{"site": "blog", "visibility": "unlisted"}},
		{"set_site_offline", map[string]any{"site": "draft", "offline": true}},
		{"set_site_passcode", map[string]any{"site": "blog", "passcode": "482915"}},
		{"set_site_access", map[string]any{"site": "blog", "access": "specific"}},
		{"list_site_viewers", map[string]any{"site": "blog"}},
		{"grant_site_viewer", map[string]any{"site": "blog", "emails": []any{"mom@example.com"}}},
		{"revoke_site_viewer", map[string]any{"site": "blog", "email": "dad@example.com"}},
		{"storage_visitor_emails", map[string]any{"site": "blog", "visitor_ids": []any{"1b4e28ba-2fa1-11d2-883f-0016d3cca427", "2b4e28ba-2fa1-11d2-883f-0016d3cca427"}}},
		{"get_site_passcode", map[string]any{"site": "blog"}},
		{"remove_site_passcode", map[string]any{"site": "blog", "confirm": true}},
		{"sign_out_site_visitors", map[string]any{"site": "blog"}},
		{"keep_site", map[string]any{"site": "blog"}},
		{"connect_domain", map[string]any{"site": "pend", "domain": "pend.example.com"}},
		{"domain_status", map[string]any{"site": "blog"}},
		{"domain_status", map[string]any{"site": "pend"}},
		{"remove_domain", map[string]any{"site": "blog", "confirm_domain": "rsvp.example.com"}},
		{"connect_domain", map[string]any{"domain": "*.trips.example.com"}},
		{"domain_status", map[string]any{"domain": "*.trips.example.com"}},
		{"remove_domain", map[string]any{"confirm_domain": "*.trips.example.com"}},
		{"site_analytics", map[string]any{"site": "blog", "days": 7}},
		{"export_site", map[string]any{"site": "blog"}},
		{"storage_list_resources", map[string]any{"site": "blog"}},
		{"storage_get_usage", map[string]any{"site": "blog"}},
		{"storage_set_resource", map[string]any{"site": "blog", "name": "store", "body": map[string]any{"kind": "kv", "rules": map[string]any{"version": 1, "policies": []any{map[string]any{"name": "read", "command": "read", "using": `auth.role == "owner"`}}}, "site_passcode": "inherit"}}},
		{"storage_delete_resource", map[string]any{"site": "blog", "name": "store"}},
		{"storage_list_kv_keys", map[string]any{"site": "blog", "name": "store", "prefix": "co"}},
		{"storage_get_kv", map[string]any{"site": "blog", "name": "store", "key": "count"}},
		{"storage_put_kv", map[string]any{"site": "blog", "name": "store", "key": "count", "value": 3}},
		{"storage_delete_kv", map[string]any{"site": "blog", "name": "store", "key": "count"}},
		{"storage_sql_query", map[string]any{"site": "blog", "name": "store", "sql": "SELECT n FROM t WHERE n=?", "params": []any{2}}},
		{"storage_sql_execute", map[string]any{"site": "blog", "name": "store", "sql": "UPDATE t SET n=?", "params": []any{2}}},
		{"storage_sql_schema", map[string]any{"site": "blog", "name": "store", "sql": "CREATE TABLE t(n)"}},
		{"storage_list_file_objects", map[string]any{"site": "blog", "name": "store", "prefix": "a"}},
		{"storage_put_file", map[string]any{"site": "blog", "name": "store", "path": "a.txt", "content_base64": "aGk=", "content_type": "text/plain"}},
		{"storage_delete_file", map[string]any{"site": "blog", "name": "store", "path": "a.txt"}},
		{"storage_file_download_link", map[string]any{"site": "blog", "name": "store", "path": "a.txt"}},
		{"get_page_recipe", map[string]any{"topic": "records", "site": "blog"}},
	}
	byName := map[string]Tool{}
	for _, tool := range append(Tools(), teamTools()...) {
		byName[tool.Name] = tool
	}
	seen := map[string]map[string]bool{}
	for _, c := range calls {
		text, structured, isErr := resultOf(t, send(t, s, toolCall(c.tool, c.args), nil, true))
		if isErr || structured == nil {
			t.Errorf("%s %v: isErr=%v structured=%v text=%s", c.tool, c.args, isErr, structured != nil, text)
			continue
		}
		if seen[c.tool] == nil {
			seen[c.tool] = map[string]bool{}
		}
		if err := ValidateOutput(byName[c.tool].OutputSchema, structured, seen[c.tool]); err != nil {
			t.Errorf("%s %v: %v\nstructuredContent: %s", c.tool, c.args, err, jsonText(structured))
		}
	}
	// The picker result needs a UI client with the flag on; check its shape directly.
	picker := photoPickerOutput("https://simple-host.app/photos?site=blog&account=alice", time.Now())
	if err := ValidateOutput(byName["create_upload_link"].OutputSchema, picker.Structured, seen["create_upload_link"]); err != nil {
		t.Errorf("picker result: %v", err)
	}
	for name := range byName {
		if seen[name] == nil {
			t.Errorf("%s was never called successfully here", name)
		}
	}
}

// A failed call is not held to the schema, and must not pretend otherwise by
// carrying structuredContent.
func TestErrorResultsCarryNoStructuredContent(t *testing.T) {
	up := &recordingUpstream{}
	s := newTestServer(up)
	for _, body := range []string{
		toolCall("get_site", map[string]any{"site": "nope"}),
		toolCall("delete_site", map[string]any{"site": "a", "confirm_name": "b"}),
		toolCall("who_am_i", map[string]any{"stray": 1}),
	} {
		res := decode(t, send(t, s, body, nil, true))["result"].(map[string]any)
		if res["isError"] != true {
			t.Fatalf("expected an error result: %v", res)
		}
		if _, has := res["structuredContent"]; has {
			t.Errorf("error result carries structuredContent: %v", res)
		}
	}
}
