package storage

import (
	"context"
	"encoding/json"
	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/internal/storage"
	"sort"
	"strings"
	"time"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func rulesInvalid() *Error {
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

func effectiveRules(c Call, table string) json.RawMessage {
	if c.Resource.TableLookup != nil {
		if own, ok := c.Resource.TableLookup(strings.ToLower(table)); ok {
			return own.Rules
		}
	}
	if own, ok := c.Resource.Tables[strings.ToLower(table)]; ok {
		return own.Rules
	}
	return c.Resource.Rules
}

// A binding-only plan is compiled without opening SQLite or taking a lock.
// Its catalog admits the identifiers in the rules; it is never used to build
// SQL. Stage 2 recompiles against the actual catalog inside admission.
func ruleBindingTable(set ruleengine.PolicySet, name string) ruleengine.Table {
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

func (s *Service) compileCallRules(c *Call, table ruleengine.Table) error {
	schema, err := s.rulesSchema()
	if err != nil {
		return rulesInvalid()
	}
	set, errs := ruleengine.DecodeSet(effectiveRules(*c, table.Name))
	if len(errs) != 0 {
		return rulesInvalid()
	}
	plan, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
	if len(errs) != 0 {
		return rulesInvalid()
	}
	c.ruleSet, c.rulePlan = set, plan
	return nil
}

func (s *Service) ruleValues(ctx context.Context, c *Call, op ruleengine.Op, refreshOwner bool) error {
	provider := s.deps.Bindings
	if provider == nil {
		provider = DefaultProvider{DB: s.db}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if refreshOwner && c.OwnerOnSite {
		owner, err := provider.OwnerOnSite(ctx, c.SiteID, c.VisitorID)
		c.OwnerOnSite = err == nil && owner
	}
	v := platformValues(*c)
	var needed []string
	declared := s.deps.ProductBindingSchema
	if declared == nil {
		declared = provider.Schema()
	}
	for _, name := range c.rulePlan.Needs(op) {
		if _, ok := declared[name]; ok {
			needed = append(needed, name)
		}
	}
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
	c.ruleValues = v
	return nil
}

func (s *Service) ruleDecision(c *Call, op ruleengine.Op) *Error {
	if c.VisitorID != "" && c.rulePlan.NeedsIdentity(op) {
		if err := s.visitorActive(c); err != nil {
			return err
		}
	}
	permitted, code := rulePermission(c.rulePlan, c.ruleSet, op, c.ruleValues, c.SpamCheck)
	if !permitted {
		if code == "sign_in_required" {
			return &Error{Code: code, Message: "sign in to this site first"}
		}
		return &Error{Code: code, Message: "read is not open to this visitor here"}
	}
	return nil
}

func (s *Service) authorizeRuleRead(ctx context.Context, c *Call, table string) *Error {
	if c.Resource.RulesStatus() != "rules" {
		return rulesInvalid()
	}
	set, errs := ruleengine.DecodeSet(effectiveRules(*c, table))
	if len(errs) != 0 {
		return rulesInvalid()
	}
	if err := s.compileCallRules(c, ruleBindingTable(set, table)); err != nil {
		return err.(*Error)
	}
	if c.VisitorID != "" && c.rulePlan.NeedsIdentity(ruleengine.OpRead) {
		if err := s.visitorActive(c); err != nil {
			return err
		}
	}
	if err := s.ruleValues(ctx, c, ruleengine.OpRead, false); err != nil {
		return err.(*Error)
	}
	return s.ruleDecision(c, ruleengine.OpRead)
}

// ruleReadPredicate is stage 2: use the actual columns and fresh trusted facts.
func (s *Service) ruleReadPredicate(ctx context.Context, c *Call, table string, meta rowMetadata) (ruleengine.Predicate, error) {
	t := ruleengine.Table{Name: table, RowKey: meta.rowid}
	for _, name := range meta.order {
		t.Columns = append(t.Columns, ruleengine.Column{Name: name})
	}
	if err := s.compileCallRules(c, t); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.ruleValues(ctx, c, ruleengine.OpRead, true); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.ruleDecision(c, ruleengine.OpRead); err != nil {
		return ruleengine.Predicate{}, err
	}
	predicate, err := c.rulePlan.Using(ruleengine.OpRead, c.ruleValues)
	if err != nil {
		if e, ok := err.(ruleengine.RuleError); ok && e.Clause == "binding" {
			return predicate, &Error{Code: "internal_error", Message: "cannot read storage bindings"}
		}
		return predicate, rulesInvalid()
	}
	return predicate, nil
}

// ensureSchemaRules preserves author columns even when some other column or
// binding has made a saved set invalid. Invalid sets become owner-facing notes;
// they continue to deny visitors until repaired.
func (s *Service) ensureSchemaRules(conn *sqlite3.Conn, c Call, before map[string]bool) ([]string, error) {
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
		set, errs := ruleengine.DecodeSet(effectiveRules(c, name))
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
		if err := s.compileCallRules(&c, table); err != nil {
			notes = append(notes, "table "+name+": rules_invalid; repair the rules for this schema")
		}
	}
	return notes, nil
}
