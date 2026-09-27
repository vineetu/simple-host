package mcp

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
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
	for name := range outputSchemas() {
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
		"fraction":         {"name": "n", "count": 1.5},
		"not in enum":      {"name": "n", "count": 1, "kind": "c"},
		"null not allowed": {"name": nil, "count": 1},
		"bad array item":   {"name": "n", "count": 1, "list": []any{map[string]any{}}},
	} {
		if err := ValidateOutput(schema, bad, nil); err == nil {
			t.Errorf("%s: accepted %v", label, bad)
		}
	}
	for label, bad := range map[string]any{
		"root not object":      map[string]any{"type": "array", "items": map[string]any{}},
		"unknown keyword":      outObject(map[string]any{"a": map[string]any{"type": "string", "format": "uri"}}),
		"required undeclared":  outObject(map[string]any{"a": outString("a")}, "b"),
		"unknown type":         outObject(map[string]any{"a": map[string]any{"type": "text"}}),
		"enum of wrong type":   outObject(map[string]any{"a": map[string]any{"type": "integer", "enum": []any{"x"}}}),
		"array without items":  outObject(map[string]any{"a": map[string]any{"type": "array"}}),
		"items on non-array":   outObject(map[string]any{"a": map[string]any{"type": "string", "items": map[string]any{}}}),
		"non-string describes": outObject(map[string]any{"a": map[string]any{"type": "string", "description": 1}}),
	} {
		if err := CheckOutputSchema(bad); err == nil {
			t.Errorf("%s: schema accepted", label)
		}
	}
}

// outputSchema arrived in 2025-06-18; a client that declared 2025-03-26 is
// listed tools without it, everyone else with it.
func TestOutputSchemaAdvertisedByRevision(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + protocolVersion + `","io.modelcontextprotocol/clientCapabilities":{}}`
	cases := []struct {
		label   string
		body    string
		headers map[string]string
		want    bool
	}{
		{"2025-03-26", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2025-03-26"}, false},
		{"2025-06-18", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2025-06-18"}, true},
		{"2025-11-25", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2025-11-25"}, true},
		{"no version declared", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil, true},
		{protocolVersion, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + meta + `}}`,
			map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/list"}, true},
	}
	for _, c := range cases {
		rec := send(t, s, c.body, c.headers, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: tools/list %d %s", c.label, rec.Code, rec.Body.String())
		}
		tools := decode(t, rec)["result"].(map[string]any)["tools"].([]any)
		if len(tools) != len(Tools()) {
			t.Fatalf("%s: %d tools listed", c.label, len(tools))
		}
		for _, raw := range tools {
			tool := raw.(map[string]any)
			schema, has := tool["outputSchema"]
			if has != c.want {
				t.Errorf("%s: %s outputSchema present=%v, want %v", c.label, tool["name"], has, c.want)
			}
			if has {
				if err := CheckOutputSchema(schema); err != nil {
					t.Errorf("%s: %s listed schema: %v", c.label, tool["name"], err)
				}
			}
			if tool["inputSchema"] == nil || tool["annotations"] == nil {
				t.Errorf("%s: %s lost its inputSchema or annotations", c.label, tool["name"])
			}
		}
	}
}

// Every tool, through every branch that shapes its output, returns
// structuredContent that its outputSchema accepts; and every property a schema
// declares is returned by at least one of these calls, so no schema describes
// a field the tool never sends. The end-to-end twin against the real
// application is TestOutputSchemasMatchRealResults in internal/handler.
func TestEveryToolResultMatchesItsOutputSchema(t *testing.T) {
	site := func(name string, version int, vis, domain, status string) string {
		m := map[string]any{"id": "s-" + name, "user_id": "u-1", "name": name, "active_version": version,
			"site_url": "https://sites.simple-host.app/ann/" + name + "/", "visibility": vis}
		if domain != "" {
			m["custom_domain"], m["domain_status"] = domain, status
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	fixed := func(status int, body string) func() (int, string) {
		return func() (int, string) { return status, body }
	}
	split := `{"person":{"views":5,"visitors":2},"bot":{"views":9,"visitors":3},"infra":{"views":1,"visitors":1},"unknown":{"views":0,"visitors":0}}`
	big := strings.Repeat("a", maxFileText+10)
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/me": fixed(200, `{"id":"u-1","username":"a@example.com","handle":"ann","display_name":"Ann",`+
			`"address":{"state":"waiting","address":"https://<site>.ann.simple-host.app/","ready_in_hours":3,"note":"Your sites are at ann.simple-host.app/<site>/ until ..."}}`),
		"GET /v1/sites": fixed(200, "["+site("blog", 2, "public", "rsvp.example.com", "active")+","+
			strings.TrimSuffix(site("draft", 0, "unlisted", "", ""), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ann.simple-host.app/<site>/ until ..."}}`+","+site("pend", 1, "public", "pend.example.com", "pending")+","+
			strings.Replace(site("broken", 1, "unlisted", "broken.example.com", "error"), "{", `{"offline":true,`, 1)+"]"),
		"GET /v1/sites/blog/versions/2/files":            fixed(200, `{"files":[{"path":"index.html","size":11},{"path":"logo.png","size":4}]}`),
		"GET /v1/sites/blog/versions/2/files/index.html": fixed(200, "<h1>hi</h1>"),
		"GET /v1/sites/blog/versions/1/files/logo.png":   fixed(200, "\x89PNG\x00\x01"),
		"GET /v1/sites/blog/versions/2/files/big.txt":    fixed(200, big),
		"POST /v1/sites/fresh/files":                     fixed(201, strings.TrimSuffix(site("fresh", 1, "unlisted", "", ""), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"PUT /v1/sites/blog/files":                       fixed(200, strings.TrimSuffix(site("blog", 3, "public", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"GET /v1/sites/blog/versions": fixed(200, `[{"version_number":1,"created_at":"2026-09-01T00:00:00Z","is_active":false,"status":"active"},`+
			`{"version_number":2,"created_at":"2026-09-02T00:00:00Z","is_active":true,"status":"active"},`+
			`{"version_number":3,"created_at":"2026-09-03T00:00:00Z","is_active":false,"status":"ready"}]`),
		"PUT /v1/sites/pend/files": fixed(200, strings.Replace(site("pend", 1, "public", "pend.example.com", "pending"), "{",
			`{"unpublished_version":2,"preview_url":"https://pend.ann.simple-host.app/__preview/2/abc.def/",`, 1)),
		"POST /v1/sites/blog/versions/3/preview-link": fixed(200, `{"site":"blog","version":3,"live":false,"url":"https://blog.ann.simple-host.app/__preview/3/abc.def/","expires_at":"2026-09-27T11:00:00Z","expires_in":3600}`),
		"PUT /v1/sites/blog/active-version":           fixed(200, strings.TrimSuffix(site("blog", 1, "public", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"DELETE /v1/sites/blog":                       fixed(204, ""),
		"GET /v1/me/deleted-sites":                    fixed(200, `{"sites":[{"name":"old","deleted_at":"2026-09-20T00:00:00Z","purge_at":"2026-09-27T00:00:00Z"}],"retention_days":7}`),
		"POST /v1/sites/old/restore":                  fixed(200, strings.TrimSuffix(site("old", 2, "unlisted", "old.example.com", "active"), "}")+`,"address_state":{"state":"waiting","note":"Your sites are at ..."}}`),
		"PATCH /v1/sites/blog":                        fixed(200, strings.TrimSuffix(site("journal", 2, "unlisted", "rsvp.example.com", "active"), "}")+`,"address_state":{"state":"failing","note":"Your sites are at ..."}}`),
		"PUT /v1/sites/blog/visibility":               fixed(200, `{"visibility":"unlisted"}`),
		"PATCH /v1/sites/draft":                       fixed(200, strings.Replace(site("draft", 0, "unlisted", "", ""), "{", `{"offline":true,`, 1)),
		"GET /v1/u/ann/sites/blog/state":              fixed(200, `{"count":2,"rsvps":["Ann"]}`),
		"PATCH /v1/u/ann/sites/blog/state":            fixed(200, `{"count":3}`),
		"PUT /v1/u/ann/sites/blog/state":              fixed(200, `["a replaced document may be any JSON"]`),
		"GET /v1/sites/blog/collections": fixed(200, `{"collections":[{"name":"rsvps","count":3,"private":false},`+
			`{"name":"orders","count":1,"private":true,"last_at":"2026-09-01T00:00:00Z"}]}`),
		"GET /v1/u/ann/sites/blog/collections/rsvps": fixed(200, `{"items":[{"id":12,"data":{"name":"Ann"},"created_at":"2026-09-03T00:00:00Z"},`+
			`{"id":11,"data":"a page may save a bare string","created_at":"2026-09-02T00:00:00Z"}],"next":11,"private":false}`),
		"GET /v1/u/ann/sites/blog/collections/orders": fixed(200, `{"items":[{"id":5,"data":{"item":"mug","_submitted_by":"v@example.com",`+
			`"_submitted_at":"2026-09-03T00:00:00Z"},"created_at":"2026-09-03T00:00:00Z","by":"v@example.com"}],"private":true}`),
		"POST /v1/u/ann/sites/blog/collections/rsvps":   fixed(201, `{"id":13,"data":{"name":"Bo"},"created_at":"2026-09-04T00:00:00Z"}`),
		"PUT /v1/sites/blog/collections/orders/privacy": fixed(200, `{"private":true,"domain":"rsvp.example.com","message":"orders is now private."}`),
		"PUT /v1/sites/blog/collections/rsvps/privacy":  fixed(200, `{"private":false}`),
		"PUT /v1/sites/blog/keep":                       fixed(200, `{"name":"blog","keep":true}`),
		"PATCH /v1/u/ann/sites/blog/collections/orders/items/5": fixed(200, `{"id":5,"data":{"item":"mug","status":"done",`+
			`"_submitted_by":"v@example.com","_submitted_at":"2026-09-03T00:00:00Z"},"created_at":"2026-09-03T00:00:00Z"}`),
		"DELETE /v1/u/ann/sites/blog/collections/orders/items/5":  fixed(204, ""),
		"DELETE /v1/u/ann/sites/blog/collections/rsvps":           fixed(200, `{"site":"blog","collection":"rsvps","deleted":3}`),
		"GET /v1/sites/blog/state/history":                        fixed(200, `{"site":"blog","history":[{"id":9,"op":"replace","by":"a@example.com","by_kind":"owner","at":"2026-09-27T00:00:00Z","size":12},{"id":8,"op":"change","by_kind":"anonymous","at":"2026-09-26T00:00:00Z","size":0}],"next":8,"undo_days":30}`),
		"GET /v1/sites/blog/state/history/9":                      fixed(200, `{"id":9,"op":"replace","by":"a@example.com","by_kind":"owner","at":"2026-09-27T00:00:00Z","size":12,"value":{"count":2}}`),
		"GET /v1/sites/blog/collections/rsvps/history":            fixed(200, `{"site":"blog","collection":"rsvps","history":[{"id":4,"item_id":12,"op":"clear","by":"a@example.com","by_kind":"owner","at":"2026-09-27T00:00:00Z","size":0}],"next":null,"undo_days":30}`),
		"POST /v1/sites/blog/state/history/9/restore":             fixed(200, `{"site":"blog","restored":9,"version":7,"state":{"count":2}}`),
		"POST /v1/sites/blog/collections/rsvps/history/4/restore": fixed(200, `{"site":"blog","collection":"rsvps","restored":4,"item":{"id":12,"data":{"name":"Ann"},"created_at":"2026-09-03T00:00:00Z"}}`),
		"GET /v1/sites/blog/collections/rsvps/deleted":            fixed(200, `{"site":"blog","collection":"rsvps","items":[{"id":12,"data":{"name":"Ann"},"created_at":"2026-09-03T00:00:00Z","deleted_at":"2026-09-27T00:00:00Z","by":"v@example.com"}],"next":"1790000000000000_12","undo_days":30}`),
		"POST /v1/sites/blog/collections/rsvps/items/12/restore":  fixed(200, `{"site":"blog","collection":"rsvps","restored":1}`),
		"POST /v1/sites/blog/collections/rsvps/deleted/restore":   fixed(200, `{"site":"blog","collection":"rsvps","restored":3}`),
		"DELETE /v1/sites/blog/collections/rsvps/deleted/12":      fixed(200, `{"site":"blog","collection":"rsvps","deleted_for_good":1}`),
		"DELETE /v1/sites/blog/collections/rsvps/deleted":         fixed(200, `{"site":"blog","collection":"rsvps","deleted_for_good":3}`),
		"PUT /v1/sites/blog/data/menu/kind":                       fixed(200, `{"site":"blog","name":"menu","kind":"content","label":"Page info","message":"Page info."}`),
		"PUT /v1/sites/blog/data/votes/kind":                      fixed(200, `{"site":"blog","name":"votes","kind":"entries","label":"Submissions","visibility":"public","one_per_person":true,"notify":"off","message":"Submissions, public."}`),
		"PUT /v1/sites/blog/data/menu":                            fixed(200, `{"name":"menu","kind":"content","data":{"soup":4},"saved_at":"2026-09-27T00:00:00Z"}`),
		"GET /v1/sites/blog/data": fixed(200, `{"site":"blog","names":[{"name":"votes","count":2,"private":false,"deleted":0,"kind":"entries","label":"Submissions","one_per_person":true,"notify":"off"},`+
			`{"name":"menu","count":1,"private":false,"deleted":0,"kind":"content","label":"Page info","one_per_person":false,"notify":"off"}],"savers":{"mode":"listed","allow":["@example.com"],"block":["x@y.org"]},"undeclared_names_take_saves":false}`),
		"PUT /v1/sites/blog/savers":        fixed(200, `{"site":"blog","mode":"listed","allow":["@example.com"],"block":[],"message":"Only listed."}`),
		"POST /v1/sites/blog/savers/block": fixed(200, `{"site":"blog","blocked":"x@y.org","block_count":1,"message":"x@y.org can no longer save."}`),
		"DELETE /v1/sites/blog/history":    fixed(200, `{"site":"blog","cleared":40}`),
		"POST /v1/sites/pend/domain": fixed(200, `{"domain":"pend.example.com","status":"pending","took_over_from":"x/y","certificate_status":"pending","previous_domain":"pend.simple-host.app",`+
			`"partner_domain":"www.pend.example.com","partner_status":"pending","dns_partner":{"type":"CNAME","host":"www.pend.example.com","value":"sites.simple-host.app"},`+
			`"dns":{"type":"CNAME","host":"pend.example.com","value":"sites.simple-host.app"},"dns_txt":{"type":"TXT","host":"_simple-host.pend.example.com","value":"sh-0123456789abcdef0123456789abcdef"}}`),
		"POST /v1/sites/blog/domain": fixed(200, `{"domain":"blog.simple-host.app","status":"active"}`),
		"GET /v1/sites/blog/domain":  fixed(200, `{"domain":"rsvp.example.com","status":"active","verified_at":"2026-09-01T00:00:00Z","dns":{"type":"A","host":"rsvp.example.com","value":"192.0.2.1"}}`),
		"GET /v1/sites/pend/domain": fixed(200, `{"domain":"pend.example.com","status":"pending","bound_at":"2026-09-01T00:00:00Z","expires_at":"2026-09-02T00:00:00Z","certificate_status":"issuing","previous_domain":"pend.simple-host.app",`+
			`"dns":{"type":"CNAME","host":"pend.example.com","value":"sites.simple-host.app"},"dns_txt":{"type":"TXT","host":"_simple-host.pend.example.com","value":"sh-0123456789abcdef0123456789abcdef"}}`),
		"GET /v1/sites/draft/domain": fixed(200, `{"domain":null,"status":null}`),
		"GET /v1/sites/broken/domain": fixed(200, `{"domain":"broken.example.com","status":"error","last_error":"HTTPS returned 502","certificate_status":"live","failing_since":"2026-09-01T00:00:00Z",`+
			`"partner_domain":"www.broken.example.com","partner_status":"not_set_up","partner_note":"www.broken.example.com does not point to this server yet","dns_partner":{"type":"CNAME","host":"www.broken.example.com","value":"sites.simple-host.app"},`+
			`"dns":{"type":"CNAME","host":"broken.example.com","value":"sites.simple-host.app"}}`),
		"DELETE /v1/sites/blog/domain":    fixed(204, ""),
		"POST /v1/sites/blog/export-link": fixed(200, `{"site":"blog","url":"https://simple-host.app/v1/export?token=abc.def","expires_at":"2026-09-27T10:10:00Z","expires_in":600}`),
		"GET /v1/sites/blog/analytics":    fixed(200, `{"range_days":7,"totals":`+split+`,"daily":[],"last_24h":`+split+`,"hourly":[],"classified_from":"2026-09-01"}`),
	}}
	s := newTestServer(up)
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"who_am_i", map[string]any{}},
		{"list_sites", map[string]any{}},
		{"get_site", map[string]any{"site": "blog"}},
		{"get_site", map[string]any{"site": "draft"}},
		{"read_site_file", map[string]any{"site": "blog", "path": "index.html"}},
		{"read_site_file", map[string]any{"site": "blog", "path": "logo.png", "version": 1}},
		{"read_site_file", map[string]any{"site": "blog", "path": "big.txt"}},
		{"create_site", map[string]any{"site": "fresh", "files": map[string]any{"index.html": "x"}}},
		{"update_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "x"}, "files_base64": map[string]any{"a.png": "AA=="}}},
		{"list_versions", map[string]any{"site": "blog"}},
		{"update_site", map[string]any{"site": "pend", "files": map[string]any{"index.html": "x"}, "publish": false}},
		{"preview_version", map[string]any{"site": "blog", "version": 3}},
		{"rollback_site", map[string]any{"site": "blog", "version": 1}},
		{"delete_site", map[string]any{"site": "blog", "confirm_name": "blog"}},
		{"rename_site", map[string]any{"site": "blog", "new_name": "journal"}},
		{"list_deleted_sites", map[string]any{}},
		{"restore_site", map[string]any{"site": "old"}},
		{"set_visibility", map[string]any{"site": "blog", "visibility": "unlisted"}},
		{"set_site_offline", map[string]any{"site": "draft", "offline": true}},
		{"keep_site", map[string]any{"site": "blog"}},
		{"get_state", map[string]any{"site": "blog"}},
		{"update_state", map[string]any{"site": "blog", "ops": []any{map[string]any{"op": "inc", "path": "count", "by": 1}}}},
		{"update_state", map[string]any{"site": "blog", "replace": map[string]any{"a": 1}}},
		{"list_collections", map[string]any{"site": "blog"}},
		{"read_collection", map[string]any{"site": "blog", "collection": "rsvps"}},
		{"read_collection", map[string]any{"site": "blog", "collection": "orders", "limit": 10}},
		{"add_to_collection", map[string]any{"site": "blog", "collection": "rsvps", "item": map[string]any{"name": "Bo"}}},
		{"set_collection_privacy", map[string]any{"site": "blog", "collection": "orders", "private": true}},
		{"set_collection_privacy", map[string]any{"site": "blog", "collection": "rsvps", "private": false}},
		{"update_collection_item", map[string]any{"site": "blog", "collection": "orders", "id": "5", "fields": map[string]any{"status": "done"}}},
		{"delete_collection_item", map[string]any{"site": "blog", "collection": "orders", "id": 5, "confirm_id": "5"}},
		{"clear_collection", map[string]any{"site": "blog", "collection": "rsvps", "confirm_collection": "rsvps"}},
		{"data_history", map[string]any{"site": "blog"}},
		{"data_history", map[string]any{"site": "blog", "version": "9"}},
		{"data_history", map[string]any{"site": "blog", "collection": "rsvps"}},
		{"restore_data", map[string]any{"site": "blog", "version": 9}},
		{"restore_data", map[string]any{"site": "blog", "collection": "rsvps", "version": "4"}},
		{"list_deleted", map[string]any{"site": "blog", "collection": "rsvps"}},
		{"restore_item", map[string]any{"site": "blog", "collection": "rsvps", "id": "12"}},
		{"restore_item", map[string]any{"site": "blog", "collection": "rsvps", "all": true}},
		{"delete_forever", map[string]any{"site": "blog", "collection": "rsvps", "id": "12", "confirm_id": "12"}},
		{"delete_forever", map[string]any{"site": "blog", "collection": "rsvps", "all": true, "confirm_collection": "rsvps"}},
		{"delete_forever", map[string]any{"site": "blog", "history": true, "confirm_site": "blog"}},
		{"connect_domain", map[string]any{"site": "pend", "domain": "pend.example.com"}},
		{"connect_domain", map[string]any{"site": "blog", "domain": "blog.simple-host.app"}},
		{"domain_status", map[string]any{"site": "blog"}},
		{"domain_status", map[string]any{"site": "draft"}},
		{"domain_status", map[string]any{"site": "broken"}},
		{"domain_status", map[string]any{"site": "pend"}},
		{"remove_domain", map[string]any{"site": "blog", "confirm_domain": "rsvp.example.com"}},
		{"site_analytics", map[string]any{"site": "blog", "days": 7}},
		{"export_site", map[string]any{"site": "blog"}},
		{"declare_data", map[string]any{"site": "blog", "name": "menu", "kind": "content"}},
		{"declare_data", map[string]any{"site": "blog", "name": "votes", "kind": "entries", "visibility": "public", "one_per_person": true}},
		{"update_data", map[string]any{"site": "blog", "name": "menu", "data": map[string]any{"soup": 4}}},
		{"list_data", map[string]any{"site": "blog"}},
		{"set_who_can_save", map[string]any{"site": "blog", "mode": "listed", "allow": []any{"@example.com"}}},
		{"block_person", map[string]any{"site": "blog", "email": "x@y.org"}},
		{"block_person", map[string]any{"site": "blog", "collection": "votes", "id": 3}},
	}
	byName := map[string]Tool{}
	for _, tool := range Tools() {
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
	for name, tool := range byName {
		if seen[name] == nil {
			t.Errorf("%s was never called successfully here", name)
			continue
		}
		var unseen []string
		for _, path := range SchemaPropertyPaths(tool.OutputSchema) {
			if !seen[name][path] {
				unseen = append(unseen, path)
			}
		}
		sort.Strings(unseen)
		if len(unseen) > 0 {
			t.Errorf("%s declares properties no call returned: %v", name, unseen)
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
