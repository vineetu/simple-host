package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/vsriram/simple-host/host/data/rules"
	"github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

const rulesSaveBytes = 256 << 10

var ruleIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func hasRules(raw *json.RawMessage) bool {
	return raw != nil && !bytes.Equal(bytes.TrimSpace(*raw), []byte("null"))
}

// hasPresetFields: the body or a table entry names a field of the removed
// presets.
func (in ResourceInput) hasPresetFields() bool {
	if in.Tables != nil {
		for _, table := range *in.Tables {
			if table.PresetFieldsPresent {
				return true
			}
		}
	}
	return in.PresetFieldsPresent
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

// configureRules validates the sent rules and sets them on x; a SQLite
// resource also has its database's tables prepared for them.
func (s *Service) configureRules(ctx context.Context, in ResourceInput, old rules.Resource, exists bool, x *rules.Resource) error {
	if in.rulesSaveSize() > rulesSaveBytes {
		return invalidRules("size", "", "rules exceed 256 KiB")
	}
	if in.Tables != nil && (x.Kind != "sqlite" || len(*in.Tables) > 50) {
		return invalidRules("shape", "", "at most 50 table sets apply to sqlite resources only")
	}
	schema, err := s.rulesSchema()
	if err != nil {
		return &Error{Code: "internal_error", Message: "invalid storage binding schema"}
	}
	def, err := decodeRules(*in.Rules, "", schema)
	if err != nil {
		return err
	}
	sets, err := decodeTableRules(in.Tables, old, schema)
	if err != nil {
		return err
	}
	x.Rules = ruleengine.EncodeSet(def)
	x.Tables = map[string]rules.TableRule{}
	for table, set := range sets {
		x.Tables[table] = rules.TableRule{Rules: ruleengine.EncodeSet(set)}
	}
	if x.Kind != "sqlite" {
		return checkPlainRules(x.Kind, def, schema)
	}
	pagesWroteSQL := exists && old.LegacyVisitorSQL() && old.Write != model.WhoOwner
	return s.prepareSQLiteRules(ctx, in.Call, *x, pagesWroteSQL, def, sets, schema)
}

// rulesSaveSize bounds the serialized rules supplied to the service,
// including table names and wrappers. Raw bytes count before decoding.
func (in ResourceInput) rulesSaveSize() int {
	size := len(*in.Rules) + len(`{"rules":}`)
	if in.Tables == nil {
		return size
	}
	// Commas separate entries, with none after the last.
	size += len(`,"tables":{}`) + max(len(*in.Tables)-1, 0)
	for name, q := range *in.Tables {
		quoted, _ := json.Marshal(name)
		size += len(quoted) + len(`:{"rules":}`)
		if q.Rules != nil {
			size += len(*q.Rules)
		}
	}
	return size
}

// decodeTableRules decodes the sent table rules in name order. When none are
// sent, a resource that already has rules keeps its stored table rules.
func decodeTableRules(tables *map[string]ResourceTableInput, old rules.Resource, schema ruleengine.BindingSchema) (map[string]ruleengine.PolicySet, error) {
	sets := map[string]ruleengine.PolicySet{}
	if tables == nil && old.Rules != nil {
		for table, q := range old.Tables {
			set, err := decodeRules(q.Rules, table, schema)
			if err != nil {
				return nil, err
			}
			sets[table] = set
		}
	}
	if tables == nil {
		return sets, nil
	}
	for _, name := range slices.Sorted(maps.Keys(*tables)) {
		q, table := (*tables)[name], strings.ToLower(name)
		switch _, seen := sets[table]; {
		case !resourceTableNameRE.MatchString(name) || strings.HasPrefix(table, "sqlite_"):
			return nil, invalidRules("shape", table, "invalid table name")
		case seen:
			return nil, invalidRules("shape", table, "two rules name the same table")
		case !hasRules(q.Rules):
			return nil, invalidRules("shape", table, "each table entry must contain rules only")
		}
		set, err := decodeRules(*q.Rules, table, schema)
		if err != nil {
			return nil, err
		}
		sets[table] = set
	}
	return sets, nil
}

// checkPlainRules compiles key-value or file rules against their fixed table.
func checkPlainRules(kind string, def ruleengine.PolicySet, schema ruleengine.BindingSchema) error {
	plan, errs := ruleengine.Compile(def, plainRulesTable(kind), schema, ruleengine.DefaultLimits())
	if len(errs) != 0 {
		return rulesFailure(errs)
	}
	if kind == "files" && plan.ReferencesBinding(ruleengine.OpAdd, "auth.checked") {
		return invalidRules("binding", "", "uploads are never spam-checked: file rules cannot use auth.checked")
	}
	return nil
}

// prepareSQLiteRules compiles the rules against every table before any
// preparation mutates the file, prepares the tables, then checks the prepared
// database still compiles. Rows pages wrote with SQL carry no author, so such
// a database must be empty before rules name visitor_id.
func (s *Service) prepareSQLiteRules(ctx context.Context, call Call, x rules.Resource, pagesWroteSQL bool, def ruleengine.PolicySet, sets map[string]ruleengine.PolicySet, schema ruleengine.BindingSchema) error {
	inspector := *s
	inspector.deps.SQLitePath = filepath.Join(call.RuntimeDir, "sqlite", x.Name+".sqlite")
	plans, err := inspector.inspectRulePlans(ctx, call, def, sets, schema, true)
	if err != nil {
		return err
	}
	referencesAuthor, err := rulesReferenceAuthor(plans, append([]ruleengine.PolicySet{def}, slices.Collect(maps.Values(sets))...), schema)
	if err != nil {
		return err
	}
	if _, err = s.prepareResourceTables(ctx, call, x, pagesWroteSQL && referencesAuthor, pagesWroteSQL, plans.OwnTables(), plans.Holds); err != nil {
		return storageFailure(err, &Error{Code: "visitor_id_required", Message: "a database pages could write with SQL must be empty before rules name visitor_id"})
	}
	if _, err = inspector.inspectRulePlans(ctx, call, def, sets, schema, false); err != nil {
		return &Error{Code: "internal_error", Message: "prepared rules did not compile"}
	}
	return nil
}

// rulesReferenceAuthor reports whether a compiled plan or any of the sets
// names visitor_id. Sets are also compiled against a probe catalog, so rules
// for a table that does not exist yet still count.
func rulesReferenceAuthor(plans rulePlans, sets []ruleengine.PolicySet, schema ruleengine.BindingSchema) (bool, error) {
	found := false
	for _, p := range plans {
		found = found || p.ReferencesColumn("visitor_id")
	}
	lim := ruleengine.DefaultLimits()
	lim.PredicateBytes, lim.PredicateArgs = rulesSaveBytes*10, rulesSaveBytes
	for _, set := range sets {
		probe, errs := ruleengine.Compile(set, provenanceProbe(set), schema, lim)
		if len(errs) != 0 {
			return false, &Error{Code: "internal_error", Message: "cannot inspect rules provenance"}
		}
		found = found || probe.ReferencesColumn("visitor_id")
	}
	return found, nil
}

// provenanceProbe is a synthetic catalog of every identifier the set names
// plus the provenance columns. It is only used to inspect provenance needs.
func provenanceProbe(set ruleengine.PolicySet) ruleengine.Table {
	t := ruleBindingTable(set, "probe")
	t.RowKey = "_rowid_"
	for _, col := range []string{"visitor_id", "approved_at"} {
		if !slices.ContainsFunc(t.Columns, func(c ruleengine.Column) bool { return c.Name == col }) {
			t.Columns = append(t.Columns, ruleengine.Column{Name: col})
		}
	}
	return t
}

// plainRulesTable is the fixed table key-value or file rules compile against.
func plainRulesTable(kind string) ruleengine.Table {
	if kind == "files" {
		return fileRulesTable
	}
	return ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}}
}

func (s *Service) inspectRulePlans(ctx context.Context, call Call, def ruleengine.PolicySet, sets map[string]ruleengine.PolicySet, schema ruleengine.BindingSchema, prospective bool) (rulePlans, error) {
	var plans rulePlans
	err := s.Inspect(ctx, call, false, func(i *Inspection) error {
		var err error
		plans, err = compileRulePlans(i, def, sets, schema, prospective)
		return err
	})
	return plans, err
}

// compileRulePlans compiles the sets against every table of an open database.
func compileRulePlans(i *Inspection, def ruleengine.PolicySet, sets map[string]ruleengine.PolicySet, schema ruleengine.BindingSchema, prospective bool) (rulePlans, error) {
	plans := rulePlans{}
	tables, err := i.Tables()
	if err != nil {
		return plans, err
	}
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		table, err := inspectedRulesTable(i, name, prospective)
		if err != nil {
			return plans, err
		}
		set, ok := sets[strings.ToLower(name)]
		if !ok {
			set = def
		}
		plan, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
		if len(errs) != 0 {
			return plans, rulesFailure(errs)
		}
		if prospective {
			if err := checkWorstCase(plan, table); err != nil {
				return plans, err
			}
		}
		plans[strings.ToLower(name)] = plan
	}
	return plans, nil
}

// inspectedRulesTable is the catalog rules compile against: the table's
// columns in storage order, plus the provenance columns a prospective save
// would add.
func inspectedRulesTable(i *Inspection, name string, prospective bool) (ruleengine.Table, error) {
	cols, order, err := i.TableXInfo(name)
	if err != nil {
		return ruleengine.Table{}, err
	}
	rowKey, err := runtime.RowID(i.conn, name, cols)
	if err != nil {
		return ruleengine.Table{}, invalidRules("column", name, err.Error())
	}
	found := map[string]bool{}
	for col, typ := range cols {
		lower := strings.ToLower(col)
		found[lower] = true
		if (lower == "visitor_id" || lower == "approved_at") && typ != "TEXT" {
			return ruleengine.Table{}, invalidRules("column", name, lower+" must be TEXT")
		}
	}
	for _, col := range []string{"visitor_id", "approved_at"} {
		if prospective && !found[col] {
			order = append(order, col)
		}
	}
	table := ruleengine.Table{Name: name, RowKey: rowKey}
	for _, col := range order {
		table.Columns = append(table.Columns, ruleengine.Column{Name: col})
	}
	return table, nil
}

// worstCaseClauses are the clauses a prospective save composes at their
// worst, in runtime.RuleWorstCase's argument order.
var worstCaseClauses = [5]struct {
	op    ruleengine.Op
	check bool
}{{ruleengine.OpRead, false}, {ruleengine.OpEdit, false}, {ruleengine.OpDelete, false}, {ruleengine.OpAdd, true}, {ruleengine.OpEdit, true}}

// checkWorstCase refuses rules whose worst-case statements on the table are
// too large to run.
func checkWorstCase(plan *ruleengine.Plan, table ruleengine.Table) error {
	var p [5]ruleengine.Predicate
	for j, clause := range worstCaseClauses {
		var err error
		if p[j], err = plan.WorstCase(clause.op, clause.check); err != nil {
			return err
		}
	}
	order := make([]string, len(table.Columns))
	for j, col := range table.Columns {
		order[j] = col.Name
	}
	err := runtime.RuleWorstCase(table.Name, table.RowKey, order, p[0], p[1], p[2], p[3], p[4])
	if errors.Is(err, runtime.ErrStatementTooLarge) {
		return invalidRules("composed", table.Name, err.Error())
	}
	return err
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
	if x.Kind != "sqlite" {
		_, errs := ruleengine.Compile(def, plainRulesTable(x.Kind), schema, ruleengine.DefaultLimits())
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
	for _, name := range slices.Sorted(maps.Keys(x.Tables)) {
		check(x.Tables[name].Rules, name)
	}
	return errs
}

// rulePlans are the compiled plans a rules save prepares tables for.
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
