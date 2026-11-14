package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

const rulesSaveBytes = 256 << 10

var ruleIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func hasRules(raw *json.RawMessage) bool {
	return raw != nil && !bytes.Equal(bytes.TrimSpace(*raw), []byte("null"))
}

func (in ResourceInput) hasPresetFields() bool {
	return in.PresetFieldsPresent || accessFields(in.AccessRequest) || in.Write != "" || in.WriteMode != "" || in.ApproveFirst != nil
}

func (in ResourceInput) hasTablePresetFields() bool {
	if in.Tables != nil {
		for _, table := range *in.Tables {
			if table.hasPresetFields() {
				return true
			}
		}
	}
	return false
}
func (q ResourceTableInput) hasPresetFields() bool {
	return q.PresetFieldsPresent || accessFields(q.AccessRequest) || q.Write != "" || q.WriteMode != "" || q.ApproveFirst != nil
}

func accessFields(q rules.AccessRequest) bool {
	return q.Preset != "" || q.Read != "" || q.Add != "" || q.Edit != "" || q.Delete != ""
}

func rulesFailure(errs []ruleengine.RuleError) *Error {
	return &Error{Code: "invalid_rules", Message: errs[0].Message, RuleError: &errs[0], RulesErrors: errs}
}

func invalidRules(clause, table, message string) *Error {
	return rulesFailure([]ruleengine.RuleError{{Table: table, Clause: clause, Position: -1, Message: message}})
}

func (s *Service) rulesSchema() (ruleengine.BindingSchema, error) {
	if s.deps.BindingSchema != nil {
		return s.deps.BindingSchema, nil
	}
	return MergeBindingSchema(s.deps.SpamCheckAvailable, s.deps.Bindings)
}

// decodeRules checks syntax even when the table has not been created yet.
func decodeRules(raw json.RawMessage, table string, schema ruleengine.BindingSchema) (ruleengine.PolicySet, error) {
	set, errs := ruleengine.DecodeSet(raw)
	if len(errs) == 0 {
		errs = ruleengine.ValidateSyntax(set, schema, ruleengine.DefaultLimits())
	}
	for i := range errs {
		errs[i].Table = table
	}
	if len(errs) != 0 {
		return set, rulesFailure(errs)
	}
	return set, nil
}

func (s *Service) configureRules(ctx context.Context, in ResourceInput, old rules.Resource, exists bool, x *rules.Resource) error {
	if x.Kind == "files" || in.hasPresetFields() {
		return invalidRules("shape", "", "rules cannot be combined with preset fields or used on files")
	}
	if x.Kind == "kv" && x.KVStore != "sqlite" {
		return &Error{Code: "rules_unavailable", Message: "this namespace has not been moved yet"}
	}
	// Bound the serialized rules supplied to the service, including table names
	// and wrappers. Raw bytes count before decoding/canonicalization.
	size := len(*in.Rules) + len(`{"rules":}`)
	if in.Tables != nil {
		size += len(`,"tables":{}`)
		separator := 0
		for name, q := range *in.Tables {
			quoted, _ := json.Marshal(name)
			size += separator + len(quoted) + len(`:{"rules":}`)
			separator = 1 // commas separate entries, with none after the last
			if q.Rules != nil {
				size += len(*q.Rules)
			}
		}
	}
	if size > rulesSaveBytes {
		return invalidRules("size", "", "rules exceed 256 KiB")
	}
	if in.Tables != nil && (x.Kind != "sqlite" || len(*in.Tables) > 50) {
		return invalidRules("shape", "", "at most 50 table sets apply to sqlite resources only")
	}
	if in.Tables == nil && old.Rules == nil && len(old.Tables) > 0 {
		return invalidRules("shape", "", "send tables with rules for each table, or tables: {}")
	}
	schema, err := s.rulesSchema()
	if err != nil {
		return &Error{Code: "internal_error", Message: "invalid storage binding schema"}
	}
	def, err := decodeRules(*in.Rules, "", schema)
	if err != nil {
		return err
	}
	sets := map[string]ruleengine.PolicySet{}
	deny := rules.AccessMatrix{Read: rules.WhoNobody, Add: rules.WhoNobody, Edit: rules.WhoNobody, Delete: rules.WhoNobody}
	x.Access, x.PresetBase = &deny, "rules"
	x.Read, x.Write, x.WriteMode = "owner", "owner", "add"
	x.Rules = ruleengine.EncodeSet(def)
	x.Tables = map[string]rules.TableRule{}
	if in.Tables != nil {
		names := make([]string, 0, len(*in.Tables))
		for name := range *in.Tables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			q := (*in.Tables)[name]
			table := strings.ToLower(name)
			if !resourceTableNameRE.MatchString(name) || strings.HasPrefix(table, "sqlite_") {
				return invalidRules("shape", table, "invalid table name")
			}
			if _, ok := sets[table]; ok {
				return invalidRules("shape", table, "two rules name the same table")
			}
			if !hasRules(q.Rules) || q.hasPresetFields() {
				return invalidRules("shape", table, "each table entry must contain rules only")
			}
			set, err := decodeRules(*q.Rules, table, schema)
			if err != nil {
				return err
			}
			sets[table] = set
			x.Tables[table] = rules.TableRule{Rules: ruleengine.EncodeSet(set), Matrix: deny, PresetBase: "rules"}
		}
	} else if old.Rules != nil {
		for table, q := range old.Tables {
			set, err := decodeRules(q.Rules, table, schema)
			if err != nil {
				return err
			}
			sets[table] = set
			x.Tables[table] = rules.TableRule{Rules: ruleengine.EncodeSet(set), Matrix: deny, PresetBase: "rules"}
		}
	}
	if x.Kind == "kv" {
		_, errs := ruleengine.Compile(def, ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}}, schema, ruleengine.DefaultLimits())
		if len(errs) != 0 {
			return rulesFailure(errs)
		}
		return nil
	}
	inspector := *s
	inspector.deps.SQLitePath = filepath.Join(in.Call.RuntimeDir, "sqlite", x.Name+".sqlite")
	// All tables are compiled before any preparation mutates the file.
	plans, err := inspector.inspectRulePlans(ctx, in.Call, def, sets, schema, true)
	if err != nil {
		return err
	}
	own, hold := plans.OwnTables(), plans.Holds
	referencesAuthor := false
	for _, p := range plans {
		referencesAuthor = referencesAuthor || p.ReferencesColumn("visitor_id")
	}
	// Missing tables still carry provenance requirements for a named author.
	for _, set := range append([]ruleengine.PolicySet{def}, policySets(sets)...) {
		// A missing table gets syntax validation only. Resolve against a synthetic
		// catalog containing every identifier solely to inspect provenance needs.
		columns := map[string]bool{"visitor_id": true, "approved_at": true}
		for _, policy := range set.Policies {
			for _, name := range ruleIdentifier.FindAllString(policy.Using+" "+policy.Check, -1) {
				columns[strings.ToLower(name)] = true
			}
		}
		probeTable := ruleengine.Table{Name: "probe", RowKey: "_rowid_"}
		for name := range columns {
			probeTable.Columns = append(probeTable.Columns, ruleengine.Column{Name: name})
		}
		lim := ruleengine.DefaultLimits()
		lim.PredicateBytes, lim.PredicateArgs = rulesSaveBytes*10, rulesSaveBytes
		probe, errs := ruleengine.Compile(set, probeTable, schema, lim)
		if len(errs) != 0 {
			return &Error{Code: "internal_error", Message: "cannot inspect rules provenance"}
		}
		referencesAuthor = referencesAuthor || probe.ReferencesColumn("visitor_id")
	}

	pagesWroteSQL := exists && old.LegacyVisitorSQL() && old.Write != rules.WhoOwner
	_, err = s.prepareResourceTables(ctx, in.Call, *x, pagesWroteSQL && referencesAuthor, pagesWroteSQL, own, hold)
	if err != nil {
		if e, ok := err.(*Error); ok {
			return e
		}
		if errors.Is(err, runtime.ErrSQLiteBusy) {
			return &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
		}
		if errors.Is(err, sqlite3.FULL) {
			return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		}
		return &Error{Code: "visitor_id_required", Message: "a database pages could write with SQL must be empty before rules name visitor_id"}
	}
	if _, err = inspector.inspectRulePlans(ctx, in.Call, def, sets, schema, false); err != nil {
		return &Error{Code: "internal_error", Message: "prepared rules did not compile"}
	}
	return nil
}

func policySets(sets map[string]ruleengine.PolicySet) []ruleengine.PolicySet {
	out := make([]ruleengine.PolicySet, 0, len(sets))
	for _, set := range sets {
		out = append(out, set)
	}
	return out
}

func (s *Service) inspectRulePlans(ctx context.Context, call Call, def ruleengine.PolicySet, sets map[string]ruleengine.PolicySet, schema ruleengine.BindingSchema, prospective bool) (rulePlans, error) {
	plans := rulePlans{}
	err := s.Inspect(ctx, call, false, func(i *Inspection) error {
		tables, err := i.Tables()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(tables))
		for name := range tables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cols, order, err := i.TableXInfo(name)
			if err != nil {
				return err
			}
			rowKey, err := runtime.RowID(i.conn, name, cols)
			if err != nil {
				return invalidRules("column", name, err.Error())
			}
			found := map[string]bool{}
			for col, typ := range cols {
				lower := strings.ToLower(col)
				found[lower] = true
				if (lower == "visitor_id" || lower == "approved_at") && typ != "TEXT" {
					return invalidRules("column", name, lower+" must be TEXT")
				}
			}
			if prospective {
				for _, col := range []string{"visitor_id", "approved_at"} {
					if !found[col] {
						order = append(order, col)
					}
				}
			}
			table := ruleengine.Table{Name: name, RowKey: rowKey}
			for _, col := range order {
				table.Columns = append(table.Columns, ruleengine.Column{Name: col})
			}
			set, ok := sets[strings.ToLower(name)]
			if !ok {
				set = def
			}
			plan, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
			if len(errs) != 0 {
				return rulesFailure(errs)
			}
			if prospective {
				var predicates [5]ruleengine.Predicate
				for j, clause := range []struct {
					op    ruleengine.Op
					check bool
				}{{ruleengine.OpRead, false}, {ruleengine.OpEdit, false}, {ruleengine.OpDelete, false}, {ruleengine.OpAdd, true}, {ruleengine.OpEdit, true}} {
					predicates[j], err = plan.WorstCase(clause.op, clause.check)
					if err != nil {
						return err
					}
				}
				if err := runtime.RuleWorstCase(name, rowKey, order, predicates[0], predicates[1], predicates[2], predicates[3], predicates[4]); err != nil {
					if errors.Is(err, runtime.ErrStatementTooLarge) {
						return invalidRules("composed", name, err.Error())
					}
					return err
				}
			}
			plans[strings.ToLower(name)] = plan
		}
		return nil
	})
	return plans, err
}

// Retained rules are checked against today's columns without preparing tables or
// replacing the saved sets. Invalid rules do not prevent a passcode-only save.
func (s *Service) retainedRulesErrors(ctx context.Context, call Call, x rules.Resource) ([]ruleengine.RuleError, error) {
	if errs := s.resourceRulesErrors(x); len(errs) != 0 {
		return errs, nil
	}
	schema, err := s.rulesSchema()
	if err != nil {
		return nil, err
	}
	def, _ := ruleengine.DecodeSet(x.Rules)
	if x.Kind == "kv" {
		_, errs := ruleengine.Compile(def, ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}}, schema, ruleengine.DefaultLimits())
		return errs, nil
	}
	sets := map[string]ruleengine.PolicySet{}
	for name, table := range x.Tables {
		sets[name], _ = ruleengine.DecodeSet(table.Rules)
	}
	inspector := *s
	inspector.deps.SQLitePath = filepath.Join(call.RuntimeDir, "sqlite", x.Name+".sqlite")
	_, err = inspector.inspectRulePlans(ctx, call, def, sets, schema, false)
	var invalid *Error
	if errors.As(err, &invalid) && invalid.Code == "invalid_rules" {
		return invalid.RulesErrors, nil
	}
	return nil, err
}

// Listings validate binding/syntax only; they never open a site's SQLite file.
func (s *Service) resourceRulesErrors(x rules.Resource) []ruleengine.RuleError {
	if x.Rules == nil {
		return nil
	}
	schema, err := s.rulesSchema()
	if err != nil {
		return []ruleengine.RuleError{{Clause: "binding", Position: -1, Message: err.Error()}}
	}
	var errs []ruleengine.RuleError
	check := func(raw json.RawMessage, table string) {
		_, err := decodeRules(raw, table, schema)
		if e, ok := err.(*Error); ok {
			errs = append(errs, e.RulesErrors...)
		}
	}
	check(x.Rules, "")
	names := make([]string, 0, len(x.Tables))
	for name := range x.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		check(x.Tables[name].Rules, name)
	}
	return errs
}

// rulePlans are the rules equivalents of the preset preparation helpers.
type rulePlans map[string]*ruleengine.Plan

func (p rulePlans) OwnTables() func(string) bool {
	return func(table string) bool { plan := p[strings.ToLower(table)]; return plan != nil && plan.AuthorTracked() }
}
func (p rulePlans) Holds(table string) bool {
	plan := p[strings.ToLower(table)]
	return plan != nil && plan.ReferencesColumn("approved_at")
}
func (p rulePlans) HasChecked() bool {
	for _, plan := range p {
		if plan.ReferencesBinding(ruleengine.OpAdd, "auth.checked") {
			return true
		}
	}
	return false
}
