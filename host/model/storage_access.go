package model

import (
	"encoding/json"
	"sort"
	"strings"
)

// WhoOwner is the older columns' "owner" value (Resource.Read, Write).
const WhoOwner = "owner"

// TableRule is one SQLite table's own rules.
type TableRule struct {
	Rules json.RawMessage
}

// Resource is a saved-data resource: its rules decide who may read, add,
// edit and delete (EffectiveRules).
type Resource struct {
	// Nil rules represent SQL NULL: a resource saved before rules.
	Rules                    json.RawMessage
	Name, Kind, SitePasscode string
	// Read, Write and WriteMode are the older columns, read only to recognise
	// a resource saved before presets (BeforePresets), whose recorded
	// authors cannot be trusted (LegacyVisitorSQL, PutStorageResource). They
	// never decide access.
	Read, Write, WriteMode string
	BeforePresets          bool
	Tables                 map[string]TableRule
	TableLookup            func(string) (TableRule, bool)
	TableRange             func(func(string, TableRule))
}

// LegacyVisitorSQL: a SQLite database saved before presets with visitor read
// or write in full mode, which pages could write with SQL. Its stored
// visitor_id values may be forged, so the first rules that name an author
// clear them (configureRules).
func (x Resource) LegacyVisitorSQL() bool {
	return x.Rules == nil && x.BeforePresets && x.Kind == "sqlite" && x.WriteMode == "full" && x.Read != "own" && (x.Read != WhoOwner || x.Write != WhoOwner)
}

// OwnerOnlyRules is what a resource of any kind with no stored rules (saved
// before rules) means: the owner, and the owner signed in on the site, may
// do everything; every other visitor is refused.
var OwnerOnlyRules = json.RawMessage(`{"version":1,"policies":[{"name":"read","command":"read","using":"auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.role == \"owner\""},{"name":"edit","command":"edit","using":"auth.role == \"owner\""},{"name":"delete","command":"delete","using":"auth.role == \"owner\""}]}`)

// EffectiveRules is the one place that says which rule set governs a
// resource, or a table of a SQLite resource: the table's own set, else the
// resource's, and OwnerOnlyRules when the resource has none stored. table ""
// (or "kv", "files") is the resource itself.
func (x Resource) EffectiveRules(table string) json.RawMessage {
	if x.Rules == nil {
		return OwnerOnlyRules
	}
	if table != "" {
		if own, ok := x.tableLookup(strings.ToLower(table)); ok {
			return own.Rules
		}
	}
	return x.Rules
}

func (x Resource) tableLookup(name string) (TableRule, bool) {
	if x.TableLookup != nil {
		return x.TableLookup(name)
	}
	r, ok := x.Tables[name]
	return r, ok
}

func (x Resource) tableRange(visit func(string, TableRule)) {
	if x.TableRange != nil {
		x.TableRange(visit)
		return
	}
	for n, r := range x.Tables {
		visit(n, r)
	}
}

// RulesStatus checks only the stored rules, not policy syntax or bindings:
// "rules", or "invalid" when a SQLite table override has none stored. A
// resource with no stored rules is "rules" (EffectiveRules).
func (x Resource) RulesStatus() string {
	valid := true
	x.tableRange(func(_ string, t TableRule) {
		if t.Rules == nil {
			valid = false
		}
	})
	if !valid {
		return "invalid"
	}
	return "rules"
}

// Fingerprint compares two loads of the same resource (storageWriteLock). The
// older columns change only with the rules, so the rules stand for them.
func (x Resource) Fingerprint() string {
	parts := []string{x.Name, x.Kind, x.SitePasscode, boolText(x.Rules != nil), string(x.Rules)}
	names := make([]string, 0, len(x.Tables))
	x.tableRange(func(n string, _ TableRule) { names = append(names, n) })
	sort.Strings(names)
	for _, n := range names {
		r, _ := x.tableLookup(n)
		parts = append(parts, n, boolText(r.Rules != nil), string(r.Rules))
	}
	return strings.Join(parts, "\x00")
}

func boolText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
