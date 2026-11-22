package data

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func RulesInvalid() *Error {
	return &Error{Code: "rules_invalid", Message: "stored rules are invalid; the owner must repair them"}
}

func platformValues(c Call) ruleengine.Values {
	id, role := ruleengine.Null(), "anon"
	if c.VisitorID != "" {
		id, role = ruleengine.Text(c.VisitorID), "visitor"
	}
	if c.OwnerOnSite {
		role = "owner"
	}
	return ruleengine.Values{
		"auth.id": id, "auth.signed_in": ruleengine.Bool(c.VisitorID != ""),
		"auth.role": ruleengine.Text(role), "auth.ai": ruleengine.Bool(c.VisitorAI),
		"auth.checked": ruleengine.Bool(c.SpamCheck == "ok"), "auth.teams": ruleengine.List(),
	}
}

// A binding-only plan is compiled without opening SQLite or taking a lock.
// Its catalog admits the identifiers in the rules; it is never used to build
// SQL. Stage 2 recompiles against the actual catalog inside admission.
func RuleBindingTable(set ruleengine.PolicySet, name string) ruleengine.Table {
	t := ruleengine.Table{Name: name, RowKey: "rowid"}
	names := map[string]bool{}
	for _, policy := range set.Policies {
		for _, name := range ruleIdentifier.FindAllString(policy.Using+" "+policy.Check, -1) {
			names[strings.ToLower(name)] = true
		}
	}
	for name := range names {
		t.Columns = append(t.Columns, ruleengine.Column{Name: name})
	}
	return t
}

func (s *Service) CompileCallRules(c *Call, table ruleengine.Table) error {
	schema, err := s.RulesSchema()
	if err != nil {
		return RulesInvalid()
	}
	set, errs := ruleengine.DecodeSet(c.Resource.EffectiveRules(table.Name))
	if len(errs) != 0 {
		return RulesInvalid()
	}
	plan, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
	if len(errs) != 0 {
		return RulesInvalid()
	}
	c.ruleSet, c.RulePlan = set, plan
	return nil
}

func (s *Service) RuleValues(ctx context.Context, c *Call, op ruleengine.Op, refreshOwner bool, additional ...ruleengine.Op) error {
	provider := s.bindings()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if refreshOwner && c.OwnerOnSite {
		owner, err := provider.OwnerOnSite(ctx, c.SiteID, c.VisitorID)
		c.OwnerOnSite = err == nil && owner
	}
	v := platformValues(*c)
	var needed []string
	declared := s.Deps.ProductBindingSchema
	if declared == nil {
		declared = provider.Schema()
	}
	seen := map[string]bool{}
	for _, operation := range append([]ruleengine.Op{op}, additional...) {
		for _, name := range c.RulePlan.Needs(operation) {
			if _, ok := declared[name]; ok && !seen[name] {
				needed = append(needed, name)
				seen[name] = true
			}
		}
	}
	sort.Strings(needed)
	if len(needed) > 0 {
		extra, err := provider.Values(ctx, BindingCall{SiteID: c.SiteID, OwnerID: c.OwnerID, SiteName: c.SiteName, UserID: c.VisitorID}, needed)
		if err != nil || ctx.Err() != nil {
			return &Error{Code: "internal_error", Message: "cannot read storage bindings"}
		}
		for _, name := range needed {
			value, ok := extra[name]
			if !ok {
				return &Error{Code: "internal_error", Message: "cannot read storage bindings"}
			}
			v[name] = value
		}
	}
	c.RuleValues = v
	return nil
}

func (s *Service) RuleDecision(c *Call, op ruleengine.Op) *Error {
	if c.VisitorID != "" && c.RulePlan.NeedsIdentity(op) {
		if err := s.VisitorActive(c); err != nil {
			return err
		}
	}
	permitted, code := rulePermission(c.RulePlan, c.ruleSet, op, c.RuleValues, c.SpamCheck)
	if !permitted {
		if code == "sign_in_required" {
			return &Error{Code: code, Message: "sign in to this site first"}
		}
		if code == "add_only" {
			return &Error{Code: code, Message: "visitors can add here, not change or delete"}
		}
		if op == ruleengine.OpAdd && code != "forbidden" {
			return &Error{Code: code} // Transport supplies the spam-check message.
		}
		action := map[ruleengine.Op]string{ruleengine.OpRead: "read", ruleengine.OpAdd: "add", ruleengine.OpEdit: "edit", ruleengine.OpDelete: "delete"}[op]
		return &Error{Code: code, Message: action + " is not open to this visitor here"}
	}
	return nil
}

// authorize is the one answer to "may this caller do op on this resource",
// for every kind (table "kv" for a namespace, "files" for a file bucket),
// before locks and idempotent replay: the site gates, then the owner's own
// tools (key, connector), which rules never limit, then the resource's rules
// for everyone else, the owner signed in on the site included
// (EffectiveRules: no stored rules means owner only). Statements and file
// checks re-run the same plan on each row inside the lock.
func (s *Service) Authorize(ctx context.Context, c *Call, table string, op ruleengine.Op) *Error {
	c.Context = ctx
	if err := s.Gate(*c, op != ruleengine.OpRead); err != nil {
		return err.(*Error)
	}
	if c.Owner {
		return nil
	}
	return s.authorizeRuleOperation(ctx, c, table, op)
}

// authorizeRuleOperation is stage 1 of authorize for a caller that rules govern.
func (s *Service) authorizeRuleOperation(ctx context.Context, c *Call, table string, op ruleengine.Op) *Error {
	if err := s.CompileBindingRules(c, table); err != nil {
		return err.(*Error)
	}
	if c.VisitorID != "" && c.RulePlan.NeedsIdentity(op) {
		if err := s.VisitorActive(c); err != nil {
			return err
		}
	}
	if err := s.RuleValues(ctx, c, op, false); err != nil {
		return err.(*Error)
	}
	if op == ruleengine.OpAdd {
		unchecked := CloneRuleValues(c.RuleValues)
		unchecked["auth.checked"] = ruleengine.Bool(false)
		c.CheckedPost = c.SpamCheck == "ok" && c.RulePlan.ReferencesBinding(op, "auth.checked") && c.RulePlan.Partial(op, unchecked) != ruleengine.True
	}
	return s.RuleDecision(c, op)
}

// ruleReadPredicate is stage 2: use the actual columns and fresh trusted facts.
func (s *Service) RuleReadPredicate(ctx context.Context, c *Call, table string, meta RowMetadata) (ruleengine.Predicate, error) {
	t := ruleengine.Table{Name: table, RowKey: meta.RowID}
	for _, name := range meta.Order {
		t.Columns = append(t.Columns, ruleengine.Column{Name: name})
	}
	if err := s.CompileCallRules(c, t); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.RuleValues(ctx, c, ruleengine.OpRead, true); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.RuleDecision(c, ruleengine.OpRead); err != nil {
		return ruleengine.Predicate{}, err
	}
	predicate, err := c.RulePlan.Using(ruleengine.OpRead, c.RuleValues)
	if err != nil {
		if e, ok := err.(ruleengine.RuleError); ok && e.Clause == "binding" {
			return predicate, &Error{Code: "internal_error", Message: "cannot read storage bindings"}
		}
		return predicate, RulesInvalid()
	}
	return predicate, nil
}

// ruleWritePlan is stage 2, after the fingerprint recheck and site admission.
// All predicates for the transaction use this one fresh binding snapshot.
func (s *Service) RuleWritePlan(c *Call, table string, meta RowMetadata, op ruleengine.Op) error {
	if c.RulePlan == nil {
		return nil
	}
	t := ruleengine.Table{Name: table, RowKey: meta.RowID}
	for _, name := range meta.Order {
		t.Columns = append(t.Columns, ruleengine.Column{Name: name})
	}
	if err := s.CompileCallRules(c, t); err != nil {
		return err
	}
	if err := s.RuleValues(c.Context, c, op, true); err != nil {
		return err
	}
	if err := s.RuleDecision(c, op); err != nil {
		return err
	}
	return nil
}

// ruleWritePredicates includes read visibility of the stored row and the AI
// author constraint.
func RuleWritePredicates(c Call, op ruleengine.Op) (ruleengine.Predicate, ruleengine.Predicate, error) {
	read, err := c.RulePlan.Using(ruleengine.OpRead, c.RuleValues)
	if err != nil {
		return read, read, RulesInvalid()
	}
	using, err := c.RulePlan.Using(op, c.RuleValues)
	if err != nil {
		return read, using, RulesInvalid()
	}
	ai := ruleAIConstraint(op, c)
	using.SQL = "(" + using.SQL + ") AND (" + ai.SQL + ")"
	using.Args = append(using.Args, ai.Args...)
	return read, using, nil
}

func RuleJSONArgs(args []ruleengine.Value) []json.RawMessage {
	params := make([]json.RawMessage, len(args))
	for i, v := range args {
		switch v.Kind {
		case ruleengine.KindNull:
			params[i] = RowJSON(nil)
		case ruleengine.KindBool:
			params[i] = RowJSON(v.Bool)
		case ruleengine.KindInt:
			params[i] = RowJSON(v.Int)
		case ruleengine.KindFloat:
			// Preserve REAL binding through the existing JSON parameter runner.
			// Re-encoding 1.0 as 1 would change TEXT affinity from "1.0" to "1".
			number := strconv.FormatFloat(v.Float, 'g', -1, 64)
			if !strings.ContainsAny(number, ".eE") {
				number += ".0"
			}
			params[i] = json.RawMessage(number)
		case ruleengine.KindText:
			params[i] = RowJSON(v.Text)
		}
	}
	return params
}

// checkWrittenRule reads the stored row (including affinity, defaults and
// generated columns) inside the mutation's transaction, before commit.
func (s *Service) CheckWrittenRule(conn *sqlite3.Conn, c Call, table string, meta RowMetadata, id int64, op ruleengine.Op) error {
	if c.RulePlan == nil {
		return nil
	}
	predicate, err := c.RulePlan.Check(op, c.RuleValues)
	if err != nil {
		return RulesInvalid()
	}
	query, args, err := runtime.RuleCheck(table, meta.RowID, ruleengine.Int(id), predicate)
	if err != nil {
		return RulesInvalid()
	}
	if err := RowReadAuthorizer(conn, false); err != nil {
		return SQLiteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return SQLiteRowFailure(err, RulesInvalid())
	}
	defer stmt.Close()
	if err := runtime.BindRuleArgs(stmt, 1, args); err != nil {
		return SQLiteRowFailure(err, RulesInvalid())
	}
	if stmt.Step() {
		return nil
	}
	if err := stmt.Err(); err != nil {
		return SQLiteRowFailure(err, RulesInvalid())
	}
	if op == ruleengine.OpEdit {
		return &Error{Code: "row_not_found", Message: "row not found"}
	}
	if err := s.RuleDecision(&c, op); err != nil {
		return err
	}
	return &Error{Code: "forbidden", Message: "add is not open to this visitor here"}
}

// ensureSchemaRules preserves author columns even when some other column or
// binding has made a saved set invalid. Invalid sets become owner-facing notes;
// they continue to deny visitors until repaired.
func (s *Service) EnsureSchemaRules(conn *sqlite3.Conn, c Call, before map[string]bool) ([]string, error) {
	tables, err := runtime.SQLTables(conn)
	if err != nil {
		return nil, err
	}
	var notes []string
	own, hold := map[string]bool{}, map[string]bool{}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		set, errs := ruleengine.DecodeSet(c.Resource.EffectiveRules(name))
		if len(errs) != 0 {
			notes = append(notes, "table "+name+": rules_invalid")
			continue
		}
		// Structural requirements survive removed product declarations. This
		// parses saved expressions but never authorizes or builds SQL from them.
		var e *ruleengine.RuleError
		own[name], hold[name], e = ruleengine.RequiredColumns(set, PlatformSchema(true), ruleengine.DefaultLimits())
		if e != nil {
			notes = append(notes, "table "+name+": rules_invalid")
		}
	}
	if err := runtime.EnsureRuleColumns(conn, before, func(t string) bool { return own[t] }, func(t string) bool { return hold[t] }); err != nil {
		return nil, err
	}
	for _, name := range names {
		cols, order, err := runtime.TableXInfo(conn, name)
		if err != nil {
			return nil, err
		}
		rowKey, err := runtime.RowID(conn, name, cols)
		if err != nil {
			notes = append(notes, "table "+name+": rules_invalid")
			continue
		}
		table := ruleengine.Table{Name: name, RowKey: rowKey}
		for _, col := range order {
			table.Columns = append(table.Columns, ruleengine.Column{Name: col})
		}
		if err := s.CompileCallRules(&c, table); err != nil {
			notes = append(notes, "table "+name+": rules_invalid; repair the rules for this schema")
		}
	}
	return notes, nil
}

// fileRulesTable is what file rules are written against, in the same words as
// key-value rules: one row per file, its path and its uploader (visitor_id,
// site_storage_files.writer_id, "" for the owner). It never becomes SQL.
var FileRulesTable = ruleengine.Table{Name: "files", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "path"}, {Name: "visitor_id"}}}

var ruleIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func (s *Service) RulesSchema() (ruleengine.BindingSchema, error) {
	if s.Deps.BindingSchema != nil {
		return s.Deps.BindingSchema, nil
	}
	return MergeBindingSchema(s.Deps.SpamCheckAvailable, s.Deps.Bindings)
}
