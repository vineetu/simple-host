package handler

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// filesRulesShapes are the seven file access shapes in production and the
// rules testdata/files-rules-convert.sql (a copy of the run's convert.sql)
// writes for each. own is visitor_id == auth.id || auth.role == "owner".
var filesRulesShapes = []struct {
	name     string
	resource map[string]string
	legacy   bool // acc_* NULL: decided by read/write/write_mode
	rules    string
}{
	{"public", map[string]string{"preset": "public"}, false, `[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]`},
	{"inbox", map[string]string{"preset": "inbox"}, false, `[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]`},
	{"private", map[string]string{"preset": "private"}, false, `[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]`},
	{"wall", map[string]string{"preset": "wall"}, false, `[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]`},
	{"records", map[string]string{"preset": "records"}, false, `[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]`},
	{"shared", map[string]string{"preset": "custom", "read": "signed-in", "add": "signed-in", "edit": "own", "delete": "own"}, false, `[{"name":"read","command":"read","using":"auth.signed_in"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"delete","command":"delete","using":"visitor_id == auth.id || auth.role == \"owner\""}]`},
	{"dropbox", map[string]string{"read": "anyone", "write": "anyone", "write_mode": "add"}, true, `[{"name":"read","command":"read","using":"true"},{"name":"add","command":"add","check":"true"},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]`},
}

// Who may list, read, upload, replace and delete alice's file in each shape is
// the same before and after convert.sql moves the resource onto rules.
func TestFilesRulesConvertKeepsAccess(t *testing.T) {
	convert, err := os.ReadFile("testdata/files-rules-convert.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := newPresetSite(t)
	for _, shape := range filesRulesShapes {
		body := map[string]string{"kind": "files"}
		for k, v := range shape.resource {
			body[k] = v
		}
		s.must(t, "PUT", "/resources/"+shape.name, body, s.key, 201)
		if shape.legacy {
			if _, err := s.a.database.Exec(`UPDATE site_storage_resources SET acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='' WHERE site_id=$1 AND name=$2`, s.sid, shape.name); err != nil {
				t.Fatal(err)
			}
		}
		s.seed(t, "files", shape.name, "alice.txt")
	}
	observe := func() map[string]string {
		seen := map[string]string{}
		answer := func(r resp) string {
			if r.status/100 == 2 {
				return string(r.body)
			}
			var e struct{ Code string }
			_ = json.Unmarshal(r.body, &e)
			return strconv.Itoa(r.status) + " " + e.Code
		}
		for _, shape := range filesRulesShapes {
			base := "/files/" + shape.name + "/objects"
			for _, caller := range []string{"owner-site", "alice", "bob", "anon"} {
				h, at := s.callers[caller], shape.name+" "+caller+" "
				seen[at+"list"] = answer(s.do(t, "GET", base, nil, h))
				seen[at+"read"] = answer(s.do(t, "GET", base+"/alice.txt", nil, h))
				r := s.do(t, "PUT", base+"/new.txt", "new", h)
				seen[at+"upload"] = answer(r)
				if r.status == 200 {
					s.must(t, "DELETE", base+"/new.txt", nil, s.key, 200)
				}
				seen[at+"replace"] = answer(s.do(t, "PUT", base+"/alice.txt", "changed", h))
				r = s.do(t, "DELETE", base+"/alice.txt", nil, h)
				seen[at+"delete"] = answer(r)
				if r.status == 200 {
					s.seed(t, "files", shape.name, "alice.txt")
				}
			}
		}
		return seen
	}
	before := observe()

	// A shape outside the approved table stops the whole conversion.
	s.must(t, "PUT", "/resources/board", map[string]string{"kind": "files", "preset": "board"}, s.key, 201)
	conn, err := s.a.database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), string(convert)); err == nil {
		t.Fatal("board converted")
	}
	// psql -v ON_ERROR_STOP=1 ends its session here, which rolls back.
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	var unconverted int
	if err := s.a.database.QueryRow(`SELECT count(*) FROM site_storage_resources WHERE site_id=$1 AND kind='files' AND rules IS NULL`, s.sid).Scan(&unconverted); err != nil || unconverted != len(filesRulesShapes)+1 {
		t.Fatalf("refused conversion changed rows: %d %v", unconverted, err)
	}
	s.must(t, "DELETE", "/resources/board", nil, s.key, 200)

	if _, err := s.a.database.Exec(string(convert)); err != nil {
		t.Fatal(err)
	}
	listed := map[string]map[string]any{}
	for _, x := range s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any) {
		listed[x.(map[string]any)["name"].(string)] = x.(map[string]any)
	}
	for _, shape := range filesRulesShapes {
		var want any
		if err := json.Unmarshal([]byte(`{"version":1,"policies":`+shape.rules+`}`), &want); err != nil {
			t.Fatal(err)
		}
		if x := listed[shape.name]; x["rules_status"] != "rules" || !reflect.DeepEqual(x["rules"], want) {
			t.Errorf("%s: %v %v", shape.name, x["rules_status"], x["rules"])
		}
	}
	after := observe()
	// The one change: on a legacy row the owner signed in on the site was an
	// ordinary visitor (storageOwnerOnSite); on rules they are the owner there,
	// as on every other resource. Visitors' answers do not change.
	changed := map[string]string{
		"dropbox owner-site list":    `"visitor_id":"` + s.aliceID + `"`,
		"dropbox owner-site replace": `"path":"alice.txt"`,
		"dropbox owner-site delete":  `{"deleted":true}`,
	}
	for key, was := range before {
		if want, ok := changed[key]; ok {
			if !strings.Contains(after[key], want) {
				t.Errorf("%s: after %s", key, after[key])
			}
		} else if after[key] != was {
			t.Errorf("%s: before %s, after %s", key, was, after[key])
		}
	}
}
