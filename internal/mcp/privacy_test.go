package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const privacyUser = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
const privacySite = "2b4e28ba-2fa1-11d2-883f-0016d3cca427"

func privacyServer() (*Server, *recordingUpstream) {
	reply := func(body string) func() (int, string) { return func() (int, string) { return 200, body } }
	sites := `[{"id":"` + privacySite + `","user_id":"` + privacyUser + `","owner_username":"account@example.com","name":"blog","active_version":1,"site_url":"https://blog.ann.simple-host.app/","visibility":"public"}]`
	access := `"site":"blog","access":"specific","viewers":[{"email":"viewer@example.com","added_at":"2026-10-01","signed_in_at":"2026-10-02"}],"viewer_limit":50,"passcode_protected":false`
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/me":                                          reply(`{"id":"` + privacyUser + `","username":"account@example.com","display_name":"Private Name","handle":"ann","public_page":"https://ann.simple-host.app/"}`),
		"GET /v1/sites":                                       reply(sites),
		"GET /v1/sites/blog/versions/1/files":                 reply(`{"files":[{"path":"index.html","size":12}]}`),
		"GET /v1/sites/blog/storage/kv/orders/keys/one":       reply(`{"key":"one","value":{"order":"cake"},"visitor_id":"` + privacyUser + `"}`),
		"GET /v1/sites/blog/storage/kv/orders/keys":           reply(`{"items":[{"key":"one","visitor_id":"` + privacyUser + `"},{"key":"anon","visitor_id":null}],"next_after":null}`),
		"GET /v1/sites/blog/storage/files/photos/objects":     reply(`{"items":[{"path":"photo.png","visitor_id":"` + privacyUser + `"}]}`),
		"POST /v1/sites/blog/storage/sqlite/orders/query":     reply(`{"columns":["id","visitor_id","VISITOR_ID","item"],"rows":[[1,"` + privacyUser + `","` + privacyUser + `","cake"],[2,null,"","bread"]]}`),
		"POST /v1/sites/blog/viewers":                         reply(`{` + access + `,"added":["requested@example.com"],"already_listed":["known@example.com"]}`),
		"PUT /v1/sites/blog/access":                           reply(`{` + access + `}`),
		"DELETE /v1/sites/blog/viewers/requested@example.com": reply(`{` + access + `,"removed":"requested@example.com"}`),
		"GET /v1/sites/blog/access":                           reply(`{` + access + `}`),
	}}
	up.answers["GET /v1/sites/blog/storage/visitors"] = func() (int, string) {
		ids := strings.Split(up.requests[len(up.requests)-1].URL.Query().Get("id"), ",")
		visitors := []map[string]any{}
		for _, id := range ids {
			row := map[string]any{"visitor_id": id, "found": id == privacyUser}
			if id == privacyUser {
				row["email"] = "sender@example.com"
			}
			visitors = append(visitors, row)
		}
		raw, _ := json.Marshal(map[string]any{"visitors": visitors})
		return 200, string(raw)
	}
	s := newTestServer(up)
	s.cfg.VisitorRefs = NewVisitorReferences([]byte("privacy test key"))
	return s, up
}

func TestHostResultsMinimizeIdentity(t *testing.T) {
	s, _ := privacyServer()
	ref, _ := s.cfg.VisitorRefs.Encode(privacySite, privacyUser)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"who_am_i", map[string]any{}},
		{"list_sites", map[string]any{}},
		{"get_site", map[string]any{"site": "blog"}},
		{"storage_get_kv", map[string]any{"site": "blog", "name": "orders", "key": "one"}},
		{"storage_list_kv_keys", map[string]any{"site": "blog", "name": "orders"}},
		{"storage_list_file_objects", map[string]any{"site": "blog", "name": "photos"}},
		{"storage_sql_query", map[string]any{"site": "blog", "name": "orders", "sql": "SELECT * FROM orders"}},
		{"grant_site_viewer", map[string]any{"site": "blog", "emails": []any{"requested@example.com", "known@example.com"}}},
		{"set_site_access", map[string]any{"site": "blog", "access": "specific"}},
		{"revoke_site_viewer", map[string]any{"site": "blog", "email": "requested@example.com"}},
		{"list_site_viewers", map[string]any{"site": "blog"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, wrapped := range []bool{false, true} {
				name, args := tc.name, tc.args
				if wrapped {
					name, args = "run_tool", map[string]any{"name": tc.name, "arguments": tc.args}
				}
				rec := send(t, s, toolCall(name, args), nil, true)
				text, out, bad := resultOf(t, rec)
				if bad {
					t.Fatal(text)
				}
				for _, forbidden := range []string{"account@example.com", privacyUser, privacySite, "signed_in_at", "Private Name"} {
					if strings.Contains(rec.Body.String(), forbidden) {
						t.Errorf("result contains %s", forbidden)
					}
				}
				if err := ValidateOutput(s.byName[tc.name].OutputSchema, out, nil); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(tc.name, "storage_") && !strings.Contains(jsonText(out), ref) {
					t.Error("missing site reference")
				}
				if tc.name == "who_am_i" && out["email"] != "" {
					t.Error("required email is not blank")
				}
				if tc.name == "grant_site_viewer" || tc.name == "set_site_access" || tc.name == "revoke_site_viewer" {
					if len(out["viewers"].([]any)) != 0 || strings.Contains(rec.Body.String(), "viewer@example.com") {
						t.Error("mutation includes viewer roster")
					}
				}
				if tc.name == "list_site_viewers" && (!strings.Contains(jsonText(out), "viewer@example.com") || !strings.Contains(jsonText(out), "added_at")) {
					t.Error("viewer listing lost requested fields")
				}
			}
		})
	}
	_, out, bad := resultOf(t, send(t, s, toolCall("who_am_i", map[string]any{"include_email": true}), nil, true))
	if bad || out["email"] != "account@example.com" {
		t.Fatal("explicit email lookup failed")
	}
}

func TestVisitorReferenceFromQueryResolves(t *testing.T) {
	s, up := privacyServer()
	_, out, bad := resultOf(t, send(t, s, toolCall("storage_sql_query", map[string]any{"site": "blog", "name": "orders", "sql": "SELECT * FROM orders"}), nil, true))
	if bad {
		t.Fatal("query failed")
	}
	rows := out["response"].(map[string]any)["rows"].([]any)
	row := rows[0].([]any)
	ref := row[1].(string)
	if ref != row[2] || rows[1].([]any)[1] != nil || rows[1].([]any)[2] != "" {
		t.Fatal("case-insensitive or anonymous identities changed")
	}
	for _, ids := range []any{[]any{ref}, `["` + ref + `"]`, []any{privacyUser}} {
		text, out, bad := resultOf(t, send(t, s, toolCall("storage_visitor_emails", map[string]any{"site": "blog", "visitor_ids": ids}), nil, true))
		if bad {
			t.Fatal(text)
		}
		v := out["visitors"].([]any)[0].(map[string]any)
		expected := ref
		if values, ok := ids.([]any); ok {
			expected = values[0].(string)
		}
		if v["visitor_id"] != expected || v["email"] != "sender@example.com" || v["found"] != true {
			t.Fatalf("lookup: %v", v)
		}
		if up.requests[len(up.requests)-1].URL.Query().Get("id") != privacyUser {
			t.Fatal("REST did not receive original ID")
		}
	}
	wrong, _ := s.cfg.VisitorRefs.Encode("another-site", privacyUser)
	_, out, bad = resultOf(t, send(t, s, toolCall("storage_visitor_emails", map[string]any{"site": "blog", "visitor_ids": []any{wrong}}), nil, true))
	if bad || out["visitors"].([]any)[0].(map[string]any)["found"] != false {
		t.Fatal("cross-site reference resolved")
	}
}

func TestVisitorReferencesAreScopedAndReversible(t *testing.T) {
	refs := NewVisitorReferences([]byte("test key"))
	a, err := refs.Encode(privacySite, privacyUser)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := refs.Encode(privacySite, privacyUser)
	other, _ := refs.Encode("other-site", privacyUser)
	if a != b || a == other || !strings.HasPrefix(a, "v_") || strings.Contains(a, privacyUser) {
		t.Fatal("reference not deterministic and site scoped")
	}
	id, err := refs.Decode(privacySite, a)
	if err != nil || id != privacyUser {
		t.Fatal("reference did not round trip")
	}
	for _, bad := range []string{"v_", "v_bad", privacyUser} {
		if _, err := refs.Decode(privacySite, bad); err == nil {
			t.Error("invalid reference accepted")
		}
	}
}

func TestRESTErrorDoesNotEchoRawBody(t *testing.T) {
	err := restError("test", upstreamResult{status: http.StatusBadGateway, body: []byte("account@example.com " + privacyUser)})
	if strings.Contains(err.Error(), privacyUser) || strings.Contains(err.Error(), "account@example.com") || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatal(err)
	}
}
