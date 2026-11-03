package storage

import (
	"fmt"
	"sort"
	"strings"
)

const (
	WhoNobody   = "nobody"
	WhoOwner    = "owner"
	WhoOwn      = "own"
	WhoSignedIn = "signed-in"
	WhoAnyone   = "anyone"
	// checked: anyone who passes the spam Check (turnstile.go); add only.
	WhoChecked = "checked"
)

// Narrowest first. Each Value includes the ones narrower than it, except
// nobody (which no page request passes).
var whoRank = map[string]int{WhoNobody: 0, WhoOwner: 1, WhoOwn: 2, WhoSignedIn: 3, WhoChecked: 4, WhoAnyone: 4}

// AccessMatrix provides the storage access matrix.
type AccessMatrix struct {
	Read   string `json:"read"`
	Add    string `json:"add"`
	Edit   string `json:"edit"`
	Delete string `json:"delete"`
}

// Preset provides the storage preset.
type Preset struct {
	ID     string
	Matrix AccessMatrix
}

// The presets, exactly as designed. private is the default. comments
// (2026-10-14) is offered only where the spam Check is on.
// Presets provides the storage presets.
var Presets = []Preset{
	{"public", AccessMatrix{"anyone", "owner", "owner", "owner"}},
	{"inbox", AccessMatrix{"owner", "anyone", "owner", "owner"}},
	{"wall", AccessMatrix{"anyone", "signed-in", "owner", "own"}},
	{"comments", AccessMatrix{"anyone", "checked", "owner", "owner"}},
	{"records", AccessMatrix{"own", "signed-in", "owner", "owner"}},
	{"personal", AccessMatrix{"own", "signed-in", "own", "own"}},
	{"board", AccessMatrix{"signed-in", "signed-in", "signed-in", "owner"}},
	{"private", AccessMatrix{"owner", "owner", "owner", "owner"}},
}

// PresetMatrix provides the storage preset matrix.
func PresetMatrix(id string) (AccessMatrix, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p.Matrix, true
		}
	}
	return AccessMatrix{}, false
}

// PresetName is the preset a Matrix is, or "custom".
func PresetName(m AccessMatrix) string {
	for _, p := range Presets {
		if p.Matrix == m {
			return p.ID
		}
	}
	return "custom"
}

// Value provides the storage value.
func (m AccessMatrix) Value(action string) string {
	switch action {
	case "read":
		return m.Read
	case "add":
		return m.Add
	case "edit":
		return m.Edit
	case "delete":
		return m.Delete
	}
	return WhoNobody
}

// Check applies rules R1 to R4. It returns "" or the refused rule's code and
// a plain hint.
// Check provides the storage check.
func (m AccessMatrix) Check() (string, string) {
	for _, v := range []string{m.Read, m.Add, m.Edit, m.Delete} {
		if _, ok := whoRank[v]; !ok {
			return "invalid_value", "each action takes nobody, owner, own, signed-in, or anyone (add also takes checked)"
		}
	}
	if m.Read == WhoChecked || m.Edit == WhoChecked || m.Delete == WhoChecked {
		return "checked_add_only", "checked (anyone who passes the spam Check) applies to add only; read takes anyone, and edit and delete stay with you"
	}
	if m.Edit == WhoAnyone || m.Delete == WhoAnyone {
		return "anonymous_change", "R1: edit and delete are never anyone; someone not signed in could overwrite or wipe everything. Use the inbox preset (anyone adds, only you change) or wall (signed-in people add and delete their own)"
	}
	if m.Add == WhoOwn {
		return "add_own", "R2: add is never own, because nothing is owned before it exists; use signed-in, and the server records who added each entry"
	}
	if (m.Add == WhoAnyone || m.Add == WhoChecked) && (m.Read == WhoOwn || m.Edit == WhoOwn || m.Delete == WhoOwn) {
		return "own_needs_sign_in", "R3: an entry added by someone not signed in has no author, so own could never apply to it; set add to signed-in"
	}
	if whoRank[m.Edit] > whoRank[m.Read] || whoRank[m.Delete] > whoRank[m.Read] {
		return "change_wider_than_read", "R4: edit and delete cannot be wider than read; nobody may change what they cannot see"
	}
	return "", ""
}

// UsesOwn provides the storage uses own.
func (m AccessMatrix) UsesOwn() bool {
	return m.Read == WhoOwn || m.Edit == WhoOwn || m.Delete == WhoOwn
}

// RecordsAuthor: entries need the server-owned visitor_id, because own
// applies or because visitors add (so the owner sees who sent what).
// RecordsAuthor provides the storage records author.
func (m AccessMatrix) RecordsAuthor() bool {
	return m.UsesOwn() || m.Add == WhoSignedIn || m.Add == WhoAnyone || m.Add == WhoChecked
}

// Narrower provides the storage narrower.
func Narrower(a, b string) string {
	if whoRank[a] < whoRank[b] {
		return a
	}
	return b
}

// TranslateOld turns the older read/write/write_mode fields into a
// Matrix, as designed (Compatibility). write anyone with full is refused.
// TranslateOld provides the storage translate old.
func TranslateOld(read, write, mode string) (AccessMatrix, string, string) {
	if read == "" {
		read = WhoOwner
	}
	if write == "" {
		write = WhoOwner
	}
	if mode == "" {
		mode = "full"
	}
	if read != "anyone" && read != "signed-in" && read != "owner" && read != "own" || !ValidPolicy(write) || mode != "full" && mode != "add" {
		return AccessMatrix{}, "invalid_value", "read takes anyone, signed-in, own, or owner; write takes anyone, signed-in, or owner; write_mode takes full or add"
	}
	var m AccessMatrix
	switch {
	case mode == "add":
		m = AccessMatrix{read, write, WhoOwner, WhoOwner}
		if read == WhoOwn && write == WhoAnyone {
			m.Add = WhoSignedIn
		}
	case write == WhoAnyone:
		return m, "anonymous_change", "write anyone with write_mode full let anyone overwrite or delete everything and is no longer offered; use the inbox preset (anyone adds, only you change) or wall (signed-in people add and delete their own)"
	case write == WhoSignedIn:
		x := Narrower(WhoSignedIn, read)
		m = AccessMatrix{read, WhoSignedIn, x, x}
	default:
		m = AccessMatrix{read, WhoOwner, WhoOwner, WhoOwner}
	}
	if code, hint := m.Check(); code != "" {
		return m, code, hint
	}
	return m, "", ""
}

// LegacyMatrix is how a resource saved before presets behaves until
// the owner saves a policy on it: exactly the old rules, unchecked (a
// write-anyone full resource stays open).
// LegacyMatrix provides the storage legacy matrix.
func LegacyMatrix(read, write, mode string) AccessMatrix {
	if mode == "add" {
		m := AccessMatrix{read, write, WhoOwner, WhoOwner}
		if read == WhoOwn && write == WhoAnyone {
			m.Add = WhoSignedIn
		}
		return m
	}
	if read == WhoOwn {
		return AccessMatrix{read, WhoOwner, WhoOwner, WhoOwner}
	}
	return AccessMatrix{read, write, write, write}
}

// OldFields expresses a Matrix in the older fields when it can, so an
// older skill reading the answer is not confused.
// OldFields provides the storage old fields.
func OldFields(m AccessMatrix) (read, write, mode string, ok bool) {
	read = m.Read
	if read == WhoNobody {
		read = WhoOwner
	}
	switch {
	case m.Add == m.Edit && m.Edit == m.Delete && ValidPolicy(m.Add):
		return read, m.Add, "full", true
	case m.Edit == WhoOwner && m.Delete == WhoOwner && ValidPolicy(m.Add):
		return read, m.Add, "add", true
	}
	return read, "", "", false
}

// TableRule is one SQLite table's own Matrix.
type TableRule struct {
	Matrix     AccessMatrix
	PresetBase string
}

// AccessRequest is a preset plus single-action overrides.
type AccessRequest struct {
	Preset string `json:"preset"`
	Read   string `json:"read"`
	Add    string `json:"add"`
	Edit   string `json:"edit"`
	Delete string `json:"delete"`
}

// Matrix provides the storage matrix.
func (q AccessRequest) Matrix() (AccessMatrix, string, string, string) {
	base := q.Preset
	if base == "" {
		base = "private"
	}
	if base == "custom" {
		if q.Read == "" || q.Add == "" || q.Edit == "" || q.Delete == "" {
			return AccessMatrix{}, "", "invalid_preset", "a custom Matrix sets all four of read, add, edit, and delete"
		}
		base = ""
	}
	m := AccessMatrix{}
	if base != "" {
		var ok bool
		if m, ok = PresetMatrix(base); !ok {
			return m, "", "invalid_preset", "preset takes public, inbox, wall, comments, records, personal, board, or private"
		}
	}
	for _, o := range []struct {
		v   string
		dst *string
	}{{q.Read, &m.Read}, {q.Add, &m.Add}, {q.Edit, &m.Edit}, {q.Delete, &m.Delete}} {
		if o.v != "" {
			*o.dst = o.v
		}
	}
	if code, hint := m.Check(); code != "" {
		return m, "", code, hint
	}
	if q.Preset == "" && q.Read == "" && q.Add == "" && q.Edit == "" && q.Delete == "" {
		base = "private"
	}
	return m, base, "", ""
}

// ValidPolicy reports whether the legacy write policy is valid.
func ValidPolicy(p string) bool { return p == "anyone" || p == "signed-in" || p == "owner" }

// Resource holds storage policy fields used by access rules.
type Resource struct {
	Name, Kind, Read, Write, SitePasscode, WriteMode, PresetBase string
	Access                                                       *AccessMatrix
	Tables                                                       map[string]TableRule
	ApproveFirst                                                 bool
}

// Matrix is the resource's own Matrix (the SQLite database default).
func (x Resource) Matrix() AccessMatrix {
	if x.Access != nil {
		return *x.Access
	}
	return LegacyMatrix(x.Read, x.Write, x.WriteMode)
}

// Legacy: saved before presets with rules the new model refuses (write
// anyone with full), or a full-mode SQLite database that pages still query
// with SQL. Either keeps working until the owner saves a policy on it.
// Legacy provides the storage legacy.
func (x Resource) Legacy() bool {
	if x.Access != nil {
		return false
	}
	return x.WriteMode == "full" && x.Write == WhoAnyone || x.LegacyVisitorSQL()
}

// LegacyVisitorSQL: pages may still send SQL to /query and /execute. Only a
// full-mode SQLite database saved before presets with visitor read or
// write; never on a resource with a Matrix.
// LegacyVisitorSQL provides the storage legacy visitor s q l.
func (x Resource) LegacyVisitorSQL() bool {
	return x.Access == nil && x.Kind == "sqlite" && x.WriteMode == "full" && x.Read != WhoOwn && (x.Read != WhoOwner || x.Write != WhoOwner)
}

// TableMatrix is the Matrix for one table: its own entry, or the database
// default.
// TableMatrix provides the storage table matrix.
func (x Resource) TableMatrix(table string) AccessMatrix {
	if x.Access != nil {
		// SQLite names are case-insensitive; rules are stored lower-case.
		if rule, ok := x.Tables[strings.ToLower(table)]; ok {
			return rule.Matrix
		}
	}
	return x.Matrix()
}

// OwnTables picks the tables that need visitor_id: nil (every table) on a
// Legacy read-own database, else those whose Matrix uses own or lets
// visitors add.
// OwnTables provides the storage own tables.
func (x Resource) OwnTables() func(string) bool {
	if x.Access == nil {
		return nil
	}
	return func(table string) bool { return x.TableMatrix(table).RecordsAuthor() }
}

// HasChecked: some add on this resource is checked (the comments preset).
// HasChecked provides the storage has checked.
func (x Resource) HasChecked() bool {
	if x.Access == nil {
		return false
	}
	if x.Access.Add == WhoChecked {
		return true
	}
	for _, t := range x.Tables {
		if t.Matrix.Add == WhoChecked {
			return true
		}
	}
	return false
}

// Holds: posts added to this table through the spam check wait for the
// owner's approval (approve_first; the approved_at column).
// Holds provides the storage holds.
func (x Resource) Holds(table string) bool {
	return x.ApproveFirst && x.Kind == "sqlite" && x.Access != nil && x.TableMatrix(table).Add == WhoChecked
}

// Fingerprint compares two loads of the same resource (storageWriteLock).
func (x Resource) Fingerprint() string {
	parts := []string{x.Name, x.Kind, x.Read, x.Write, x.SitePasscode, x.WriteMode, x.PresetBase, fmt.Sprint(x.ApproveFirst)}
	if x.Access != nil {
		parts = append(parts, x.Access.Read, x.Access.Add, x.Access.Edit, x.Access.Delete)
	} else {
		parts = append(parts, "-")
	}
	names := make([]string, 0, len(x.Tables))
	for n := range x.Tables {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := x.Tables[n]
		parts = append(parts, n, r.Matrix.Read, r.Matrix.Add, r.Matrix.Edit, r.Matrix.Delete, r.PresetBase)
	}
	return strings.Join(parts, "\x00")
}
