package handler

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
)

func TestStorageRulesCopies(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"version":1,"policies":[]}`), json.RawMessage(`{"version":1,"policies":[{"name":"public_read","command":"read","using":"true"}]}`)} {
		deny := model.AccessMatrix{Read: "nobody", Add: "nobody", Edit: "nobody", Delete: "nobody"}
		r := model.Resource{Name: "data", Kind: "sqlite", Read: "owner", Write: "owner", WriteMode: "add", PresetBase: "rules", Access: &deny,
			Rules: raw, ConvertedFrom: raw, Tables: map[string]model.TableRule{"posts": {Matrix: deny, PresetBase: "rules", Rules: raw}},
		}
		copied := fromRulesResource(r)
		if !bytes.Equal(copied.Rules, raw) || !bytes.Equal(copied.ConvertedFrom, raw) || !bytes.Equal(copied.Tables["posts"].Rules, raw) {
			t.Fatal("handler copy lost rules or conversion history")
		}
		back := copied.rulesResource()
		table, ok := back.TableLookup("posts")
		if !ok || !bytes.Equal(back.Rules, raw) || !bytes.Equal(back.ConvertedFrom, raw) || !bytes.Equal(table.Rules, raw) {
			t.Fatal("model copy lost rules or conversion history")
		}
		back.TableRange(func(_ string, table model.TableRule) {
			if !bytes.Equal(table.Rules, raw) {
				t.Fatal("table range lost rules")
			}
		})
		if back.RulesStatus() != r.RulesStatus() || back.Fingerprint() != r.Fingerprint() || copied.fingerprint() != r.Fingerprint() {
			t.Fatal("copy changed structural status or fingerprint")
		}
		if (back.Rules == nil) != (raw == nil) || (table.Rules == nil) != (raw == nil) || (back.ConvertedFrom == nil) != (raw == nil) {
			t.Fatal("copy changed NULL representation")
		}
	}
}

func TestStorageRulesTableFingerprint(t *testing.T) {
	x := storageResource{Name: "data", Tables: map[string]storageTableRule{"posts": {}}}
	null := x.fingerprint()
	table := x.Tables["posts"]
	table.Rules = json.RawMessage(`{"version":1,"policies":[]}`)
	x.Tables["posts"] = table
	empty := x.fingerprint()
	table.Rules = json.RawMessage(`{"version":1,"policies":[{"name":"public_read","command":"read","using":"true"}]}`)
	x.Tables["posts"] = table
	if null == empty || empty == x.fingerprint() {
		t.Fatal("table-only rules change did not change fingerprint")
	}
}
