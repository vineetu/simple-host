package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// The operator's migrate-rules command. A dry run compares, in memory, what
// each kind of caller is allowed today with what the converted rules would
// allow, and writes a plan. Apply converts exactly what the plan holds, after
// checking under the exclusive maintenance lock that nothing changed since.
// Revert restores what apply recorded. Nothing here is a route or a tool.

var (
	// ErrConvertAttention: a dry run found a difference or a resource that
	// needs a person.
	ErrConvertAttention = errors.New("the dry run found differences or resources that need a person")
	// ErrConvertIncomplete: apply or revert skipped or failed a resource.
	ErrConvertIncomplete = errors.New("some resources were skipped or failed")
	// ErrConvertPlan: the plan file cannot be used.
	ErrConvertPlan = errors.New("invalid plan")
)

const convertPlanVersion = 1

// The headroom apply's additive SQLite step may grow a database by. The step
// is run by the operator with the install stopped, so it is not held to the
// site's own allowance, only to this bound.
const convertPrepareHeadroom = 256 << 20

// ConvertPlan is what a dry run writes and what apply and revert read.
type ConvertPlan struct {
	Version     int                 `json:"version"`
	Commit      string              `json:"commit"`
	Created     string              `json:"created"`
	Resources   []ConvertPlanEntry  `json:"resources"`
	NeedsPerson []ConvertPlanNote   `json:"needs_a_person"`
	Differences []ConvertDifference `json:"differences"`
	Notes       []ConvertPlanNote   `json:"notes"`
}

// ConvertPlanEntry is one resource the dry run found safe to convert.
type ConvertPlanEntry struct {
	SiteID       string                     `json:"site_id"`
	Account      string                     `json:"account"`
	Site         string                     `json:"site"`
	Name         string                     `json:"name"`
	Kind         string                     `json:"kind"`
	Fingerprint  string                     `json:"fingerprint"`
	SchemaDigest string                     `json:"schema_digest"`
	Original     repo.ConvertOriginal       `json:"original"`
	Rules        json.RawMessage            `json:"rules"`
	Tables       map[string]json.RawMessage `json:"tables"`
	SHA256       string                     `json:"sha256"`
}

// ConvertPlanNote names a resource and says something about it.
type ConvertPlanNote struct {
	SiteID   string `json:"site_id"`
	Account  string `json:"account"`
	Site     string `json:"site"`
	Resource string `json:"resource"`
	Reason   string `json:"reason"`
}

// ConvertDifference is one answer that conversion would change.
type ConvertDifference struct {
	Account  string `json:"account"`
	Site     string `json:"site"`
	Resource string `json:"resource"`
	Table    string `json:"table"`
	Caller   string `json:"caller"`
	Action   string `json:"action"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

// Line is the dry run's printed form: account site resource table caller
// action old new.
func (d ConvertDifference) Line() string {
	return strings.Join([]string{d.Account, d.Site, d.Resource, d.Table, d.Caller, d.Action, d.Old, d.New}, " ")
}

// convertRecord is what rules_converted_from holds. The two digests let a
// rerun recognise the schema its own additive step left behind.
type convertRecord struct {
	Version              int                  `json:"version"`
	Original             repo.ConvertOriginal `json:"original"`
	Commit               string               `json:"commit"`
	RulesSHA256          string               `json:"rules_sha256"`
	PlanEntrySHA256      string               `json:"plan_entry_sha256"`
	SchemaDigest         string               `json:"schema_digest"`
	PreparedSchemaDigest string               `json:"prepared_schema_digest"`
}

// RulesConverter runs the three modes. boundary is set only by package tests,
// to stop a run after a step.
type RulesConverter struct {
	DB                 *sql.DB
	db                 repo.KVMoveDB
	DataDir            string
	Commit             string
	SpamCheckAvailable bool
	boundary           func(string) error
	euid               func() int
}

// lockMaintenance checks the user and install before creating a lock file,
// then holds both the directory and database exclusively for this run.
// All database work uses the locked connection: losing it must stop changes.
func (m *RulesConverter) lockMaintenance(ctx context.Context) (*RulesConverter, func(), error) {
	euid := os.Geteuid
	if m.euid != nil {
		euid = m.euid
	}
	if err := runtime.CheckDataDirOwner(m.DataDir, euid()); err != nil {
		return nil, nil, err
	}
	if err := CheckInstallIdentity(ctx, m.DB, m.DataDir); err != nil {
		return nil, nil, err
	}
	lock, err := runtime.LockMaintenance(m.DataDir, true)
	if err != nil {
		return nil, nil, err
	}
	install, err := repo.LockInstall(ctx, m.DB, true)
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	done := func() {
		install.Close()
		lock.Close()
	}
	run := *m
	run.db = install.Conn()
	if err := CheckInstallIdentity(ctx, run.db, m.DataDir); err != nil {
		done()
		return nil, nil, err
	}
	return &run, done, nil
}

func (m *RulesConverter) at(stage string) error {
	if m.boundary != nil {
		return m.boundary(stage)
	}
	return nil
}

func convertEncode(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func (e ConvertPlanEntry) digest() string {
	e.SHA256 = ""
	sum := sha256.Sum256(convertEncode(e))
	return hex.EncodeToString(sum[:])
}

func convertField(h io.Writer, field string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(field)))
	h.Write(size[:])
	io.WriteString(h, field)
}

// convertSetsSHA hashes the canonical form of a default set and its table
// sets, so bytes that Postgres reformatted compare equal to the plan's.
func convertSetsSHA(def json.RawMessage, tables map[string]json.RawMessage) (string, error) {
	h := sha256.New()
	add := func(name string, raw json.RawMessage) error {
		set, errs := ruleengine.DecodeSet(raw)
		if len(errs) != 0 {
			return fmt.Errorf("rules do not decode")
		}
		convertField(h, name)
		convertField(h, string(ruleengine.EncodeSet(set)))
		return nil
	}
	if err := add("", def); err != nil {
		return "", err
	}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := add(name, tables[name]); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func resourceSetsSHA(x rules.Resource) (string, error) {
	if x.Rules == nil {
		return "", fmt.Errorf("no rules")
	}
	tables := map[string]json.RawMessage{}
	for name, t := range x.Tables {
		if t.Rules == nil {
			return "", fmt.Errorf("table without rules")
		}
		tables[name] = t.Rules
	}
	return convertSetsSHA(x.Rules, tables)
}

// WriteConvertPlan writes the plan for its owner only, replacing any file of
// that name in one rename.
func WriteConvertPlan(path string, plan ConvertPlan) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".migrate-rules-plan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b.Bytes())
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ReadConvertPlan reads a plan and refuses one whose entries do not carry
// their own SHA-256.
func ReadConvertPlan(path string) (ConvertPlan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ConvertPlan{}, err
	}
	return DecodeConvertPlan(bytes.NewReader(raw))
}

// DecodeConvertPlan validates a plan from an already opened reader, so the
// operator command can check the file before reading its contents.
func DecodeConvertPlan(r io.Reader) (ConvertPlan, error) {
	var plan ConvertPlan
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return plan, fmt.Errorf("%w: %v", ErrConvertPlan, err)
	}
	if plan.Version != convertPlanVersion {
		return plan, fmt.Errorf("%w: version %d", ErrConvertPlan, plan.Version)
	}
	compact := func(raw json.RawMessage) (json.RawMessage, error) {
		var b bytes.Buffer
		if err := json.Compact(&b, raw); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	for i := range plan.Resources {
		e := &plan.Resources[i]
		var err error
		if e.Rules, err = compact(e.Rules); err != nil {
			return plan, fmt.Errorf("%w: %s/%s rules", ErrConvertPlan, e.SiteID, e.Name)
		}
		for name, set := range e.Tables {
			if e.Tables[name], err = compact(set); err != nil {
				return plan, fmt.Errorf("%w: %s/%s table %s", ErrConvertPlan, e.SiteID, e.Name, name)
			}
		}
		if e.SHA256 == "" || e.digest() != e.SHA256 {
			return plan, fmt.Errorf("%w: entry %s/%s does not match its SHA-256", ErrConvertPlan, e.SiteID, e.Name)
		}
	}
	return plan, nil
}

// convertTable is one real table as SQLite describes it.
type convertTable struct {
	name            string
	info            [][]*string       // PRAGMA table_info rows; nil is SQL NULL
	types           map[string]string // every declared column, generated ones included
	authorCollation string
	parents         []string // foreign-key parent tables; schema metadata only
}

func (t convertTable) has(column string) bool {
	for name := range t.types {
		if strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}

// convertSchemaDigest is SHA-256 over each table's name and its table_info
// rows, tables in name order.
func convertSchemaDigest(tables []convertTable) string {
	h := sha256.New()
	for _, t := range tables {
		convertField(h, t.name)
		convertField(h, strconv.Itoa(len(t.info)))
		for _, row := range t.info {
			for _, v := range row {
				if v == nil {
					h.Write([]byte{0})
					continue
				}
				h.Write([]byte{1})
				convertField(h, *v)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The key-value store's schema is fixed (section 3.8 of the rules design).
var convertKVDigest = func() string {
	sum := sha256.Sum256([]byte("kv(key,value,visitor_id)"))
	return hex.EncodeToString(sum[:])
}()

// convertPreparedDigest is the digest the schema has once apply's additive
// step has run: visitor_id TEXT appended to author-tracked tables that lack
// it, then approved_at TEXT where a set names it.
func convertPreparedDigest(tables []convertTable, own, hold func(string) bool) string {
	text, zero := "TEXT", "0"
	out := make([]convertTable, len(tables))
	for i, t := range tables {
		t.info = append([][]*string(nil), t.info...)
		for _, add := range []struct {
			column string
			need   bool
		}{{"visitor_id", own(t.name)}, {"approved_at", hold(t.name)}} {
			if add.need && !t.has(add.column) {
				cid, name := strconv.Itoa(len(t.info)), add.column
				t.info = append(t.info, []*string{&cid, &name, &text, &zero, nil, &zero})
			}
		}
		out[i] = t
	}
	return convertSchemaDigest(out)
}

func kvRuleTable() ruleengine.Table {
	return ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}}
}

// convertExam is everything known about one resource at one moment.
type convertExam struct {
	res         repo.ConvertResource
	x           rules.Resource
	original    repo.ConvertOriginal
	needs       []string
	def         ruleengine.PolicySet
	sets        map[string]ruleengine.PolicySet
	tables      []convertTable
	plans       rulePlans
	kvPlan      *ruleengine.Plan
	digest      string
	prepared    string
	differences []ConvertDifference
}

func (m *RulesConverter) siteDir(r repo.ConvertResource) string {
	if r.Deleted {
		return filepath.Join(m.DataDir, "deleted", r.OwnerID, r.SiteID)
	}
	return filepath.Join(m.DataDir, "by-id", r.OwnerID, r.SiteName)
}

func (m *RulesConverter) sqlitePath(r repo.ConvertResource) string {
	return filepath.Join(m.siteDir(r), "runtime", "sqlite", r.Name+".sqlite")
}

// inspect reads the schema and plans from one connection. Dry runs use
// immutable inspection so no sidecars change. Apply holds the exclusive
// maintenance lock and can use a WAL-aware read, including crash recovery.
func (m *RulesConverter) inspect(ctx context.Context, r repo.ConvertResource, def *ruleengine.PolicySet, sets map[string]ruleengine.PolicySet, schema ruleengine.BindingSchema, prospective, maintenance bool) ([]convertTable, rulePlans, error) {
	open := runtime.OpenInspectConn
	if maintenance {
		open = func(ctx context.Context, path string, timeout time.Duration) (*sqlite3.Conn, func(), error) {
			return runtime.OpenTableConn(ctx, path, false, timeout, 0, nil)
		}
	}
	conn, done, err := open(ctx, m.sqlitePath(r), 30*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the database: %w", err)
	}
	defer done()
	i := &Inspection{conn: conn}
	found, err := i.Tables()
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []convertTable
	for _, name := range names {
		t := convertTable{name: name}
		if t.types, _, err = i.TableXInfo(name); err != nil {
			return nil, nil, err
		}
		for column := range t.types {
			if strings.EqualFold(column, "visitor_id") {
				_, t.authorCollation, _, _, _, err = conn.TableColumnMetadata("main", name, column)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		stmt, _, err := conn.Prepare("PRAGMA table_info(" + runtime.Quote(name) + ")")
		if err != nil {
			return nil, nil, err
		}
		for stmt.Step() {
			row := make([]*string, stmt.ColumnCount())
			for c := range row {
				if stmt.ColumnType(c) != sqlite3.NULL {
					v := stmt.ColumnText(c)
					row[c] = &v
				}
			}
			t.info = append(t.info, row)
		}
		err = stmt.Err()
		stmt.Close()
		if err != nil {
			return nil, nil, err
		}
		stmt, _, err = conn.Prepare(`SELECT DISTINCT "table" FROM pragma_foreign_key_list(?) ORDER BY "table"`)
		if err != nil {
			return nil, nil, err
		}
		if err = stmt.BindText(1, name); err != nil {
			stmt.Close()
			return nil, nil, err
		}
		for stmt.Step() {
			t.parents = append(t.parents, stmt.ColumnText(0))
		}
		err = stmt.Err()
		stmt.Close()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, t)
	}
	if def == nil {
		return out, nil, nil
	}
	plans, err := compileRulePlans(i, *def, sets, schema, prospective)
	return out, plans, err
}

// convertReasons are the cases a person must decide: a rules save over such
// a resource would clear recorded authors, refuse, or end something pages do
// today. The command never does any of that.
func convertReasons(x rules.Resource, original repo.ConvertOriginal) []string {
	var out []string
	if x.Kind == "kv" && x.Access == nil && x.WriteMode == "full" && x.Write != rules.WhoOwner {
		out = append(out, "saving rules would clear the writers recorded in this key-value namespace from before presets")
	}
	if x.LegacyVisitorSQL() {
		if x.Write != rules.WhoOwner {
			out = append(out, "pages could write this database with SQL: saving rules would clear its visitor_id values, or refuse unless it is empty")
		} else {
			out = append(out, "pages still read this database with SQL, which a rules resource does not accept")
		}
	}
	if x.Access == nil && len(original.Tables) != 0 {
		out = append(out, "it has table presets but no preset of its own")
	}
	return out
}

func convertInput(x rules.Resource) ruleengine.ConvertInput {
	in := ruleengine.ConvertInput{Kind: x.Kind, Default: ruleengine.Matrix(x.Matrix()), ApproveFirst: x.ApproveFirst, Tables: map[string]ruleengine.Matrix{}}
	if x.Access != nil {
		for name, t := range x.Tables {
			in.Tables[name] = ruleengine.Matrix(t.Matrix)
		}
	}
	return in
}

// examine reads a resource's settings and schema to decide whether it can
// be converted and what its converted sets are. compare adds the
// caller-by-caller comparison; maintenance permits WAL sidecar recovery.
func (m *RulesConverter) examine(ctx context.Context, r repo.ConvertResource, compare, maintenance bool) (convertExam, error) {
	e := convertExam{res: r}
	var err error
	if e.x, err = repo.LoadResource(ctx, m.db, r.SiteID, r.Name); err != nil {
		return e, err
	}
	if e.original, err = repo.LoadConvertOriginal(ctx, m.db, r.SiteID, r.Name); err != nil {
		return e, err
	}
	if e.x.Kind != "sqlite" && e.x.Kind != "kv" {
		return e, fmt.Errorf("a %s resource never takes rules", e.x.Kind)
	}
	if e.x.Rules != nil {
		return e, nil // already on rules; the caller decides what that means
	}
	if e.x.RulesStatus() == "invalid" {
		e.needs = append(e.needs, "the stored rules state is invalid; conversion would change rules_invalid refusals")
		return e, nil
	}
	out := ruleengine.Convert(convertInput(e.x))
	if e.needs = append(convertReasons(e.x, e.original), out.Manual...); len(e.needs) != 0 {
		return e, nil
	}
	e.def, e.sets = out.Default, out.Tables
	schema := PlatformSchema(m.SpamCheckAvailable)
	refused := func(errs []ruleengine.RuleError) {
		for _, re := range errs {
			reason := re.Message
			if re.Table != "" {
				reason = "table " + re.Table + ": " + reason
			}
			e.needs = append(e.needs, "the converted rules do not compile here: "+reason)
		}
	}
	// Bindings must be valid even when no real table uses a set yet.
	for _, set := range append([]ruleengine.PolicySet{e.def}, policySets(e.sets)...) {
		if errs := ruleengine.ValidateSyntax(set, schema, ruleengine.DefaultLimits()); len(errs) != 0 {
			refused(errs)
			return e, nil
		}
	}
	if e.x.Kind == "kv" {
		var errs []ruleengine.RuleError
		if e.kvPlan, errs = ruleengine.Compile(e.def, kvRuleTable(), schema, ruleengine.DefaultLimits()); len(errs) != 0 {
			refused(errs)
			return e, nil
		}
		e.digest, e.prepared = convertKVDigest, convertKVDigest
	} else {
		e.tables, e.plans, err = m.inspect(ctx, r, &e.def, e.sets, schema, true, maintenance)
		if err != nil {
			var invalid *Error
			if errors.As(err, &invalid) && invalid.Code == "invalid_rules" {
				refused(invalid.RulesErrors)
				return e, nil
			}
			return e, err
		}
		for _, t := range e.tables {
			// Preset foreign-key checks cast the parent's author to text;
			// rules require text storage. Even a BINARY TEXT column can hold
			// a BLOB author, so schema alone cannot prove these checks equal.
			for _, parent := range t.parents {
				if e.x.TableMatrix(parent).Read == rules.WhoOwn {
					e.needs = append(e.needs, "table "+t.name+": foreign-key parent "+parent+" uses own reads; author storage types can change reference checks under rules")
				}
				// Anyone adds skip the active-account check. Preset parent
				// reads only need an ID or OwnerOnSite; rules reject a
				// suspended caller in either case.
				read := e.x.TableMatrix(parent).Read
				if e.x.TableMatrix(t.name).Add == rules.WhoAnyone && (read == rules.WhoSignedIn || read == rules.WhoOwner) {
					e.needs = append(e.needs, "table "+t.name+": foreign-key parent "+parent+" uses "+read+" reads; suspended callers can lose add access under rules")
				}
			}
			// A new server column changes row metadata and stamping, and can
			// exceed the visitor column limit. Revert keeps added columns, so
			// only prepare existing tables whose required columns are present.
			for _, add := range []struct {
				column string
				need   bool
			}{{"visitor_id", e.plans.OwnTables()(t.name)}, {"approved_at", e.plans.Holds(t.name)}} {
				if add.need && !t.has(add.column) {
					e.needs = append(e.needs, "table "+t.name+": adding "+add.column+" changes row responses or stamping")
				}
			}
			// Preset requests use table_info, while rules requests use
			// table_xinfo. Extra columns change row responses and can also
			// push a readable table over the request's column limit.
			if len(t.types) != len(t.info) {
				e.needs = append(e.needs, "table "+t.name+": generated or hidden columns change row request metadata under rules")
			}
			// Presets recognize these columns by their lowercase spelling;
			// rules resolve them case-insensitively for redaction and stamping.
			for _, row := range t.info {
				column := *row[1]
				lower := strings.ToLower(column)
				switch lower {
				case "visitor_id", "written_by", "created_at", "updated_at", "approved_at":
					if column != lower {
						e.needs = append(e.needs, "table "+t.name+": noncanonical system column "+column+" changes row responses or stamping under rules")
					}
				}
			}
			// Preset ownership SQL inherits the column's collation. Rules
			// compare author identities with BINARY semantics instead.
			if t.authorCollation != "" && !strings.EqualFold(t.authorCollation, "BINARY") {
				e.needs = append(e.needs, "table "+t.name+": visitor_id collation "+t.authorCollation+" changes ownership comparisons under rules")
			}
		}
		if len(e.needs) != 0 {
			return e, nil
		}
		e.digest = convertSchemaDigest(e.tables)
		e.prepared = convertPreparedDigest(e.tables, e.plans.OwnTables(), e.plans.Holds)
	}
	if compare {
		if e.differences, err = e.compare(schema); err != nil {
			return e, err
		}
	}
	return e, nil
}

// The callers of the decision-level oracle (R2-S6), each with and without a
// passed spam check.
var convertCallers = []struct {
	name string
	call Call
}{
	{"anonymous", Call{}},
	{"signed-in-non-author", Call{VisitorID: "other"}},
	{"author", Call{VisitorID: "author"}},
	{"owner-on-site", Call{VisitorID: "owner", OwnerOnSite: true}},
	{"visitor-AI-author", Call{VisitorID: "author", VisitorAI: true}},
	{"visitor-AI-non-author", Call{VisitorID: "other", VisitorAI: true}},
}

var convertSpam = []struct{ label, code string }{{"", ""}, {"+spam-check-passed", "ok"}, {"+spam-check-failed", "spam_check_failed"}}

var convertActions = []struct {
	name string
	op   ruleengine.Op
}{{"read", ruleengine.OpRead}, {"add", ruleengine.OpAdd}, {"edit", ruleengine.OpEdit}, {"delete", ruleengine.OpDelete}}

// convertRowDecision is the complete decision for one row: read visibility,
// the operation's own predicate, and the author term enforcement adds for a
// visitor's AI.
func convertRowDecision(p *ruleengine.Plan, op ruleengine.Op, c Call, row ruleengine.Row) (bool, error) {
	v := platformValues(c)
	if op == ruleengine.OpAdd {
		return p.EvalCheck(op, v, row)
	}
	ok, err := p.EvalUsing(ruleengine.OpRead, v, row)
	if err != nil {
		return false, err
	}
	if op != ruleengine.OpRead {
		change, err := p.EvalUsing(op, v, row)
		if err != nil {
			return false, err
		}
		ok = ok && change
	}
	if ai := ruleAIConstraint(op, c); len(ai.Args) != 0 {
		ok = ok && row["visitor_id"].Kind == ruleengine.KindText && row["visitor_id"].Text == ai.Args[0].Text
	}
	return ok, nil
}

// convertCompare is the oracle's three assertions for one table: permission,
// first refusal code, and the complete decision on each synthetic row kind. It
// reads no row and calls nothing outside this process. differ is called once
// per answer that conversion would change.
func convertCompare(x rules.Resource, table string, columns map[string]string, set ruleengine.PolicySet, p *ruleengine.Plan, differ func(caller, action, old, new string)) error {
	m := x.TableMatrix(table)
	hold := x.Holds(table)
	answer := func(allowed bool, code string) string {
		if allowed {
			return "allowed"
		}
		return "refused:" + code
	}
	visible := func(kind string, ok bool) string {
		if ok {
			return "row-" + kind + ":yes"
		}
		return "row-" + kind + ":no"
	}
	s := &Service{}
	for _, caller := range convertCallers {
		for _, spam := range convertSpam {
			for _, action := range convertActions {
				c := caller.call
				c.ActiveChecked, c.SpamCheck, c.CurrentMatrix, c.Resource = true, spam.code, m, x
				oldCall := c
				// Match the HTTP handler's storageOwnerOnSite: resources
				// from before presets treat the site owner as a visitor.
				if x.Access == nil {
					oldCall.OwnerOnSite = false
				}
				filter, oldAllowed, refusals := s.allowDecisions(&oldCall, action.name, m.Value(action.name))
				oldCode := ""
				if len(refusals) > 0 {
					oldCode = refusals[0].Code
				}
				newAllowed, newCode := rulePermission(p, set, action.op, platformValues(c), spam.code)
				name := caller.name + spam.label
				if oldAllowed != newAllowed || oldCode != newCode {
					differ(name, action.name, answer(oldAllowed, oldCode), answer(newAllowed, newCode))
				}
				for _, item := range []struct {
					kind             string
					author, approval ruleengine.Value
				}{
					{"own", ruleengine.Text(c.VisitorID), ruleengine.Text("approved")},
					{"other", ruleengine.Text("another"), ruleengine.Text("approved")},
					{"no-author", ruleengine.Null(), ruleengine.Text("approved")},
					{"held", ruleengine.Text(c.VisitorID), ruleengine.Null()},
				} {
					row := ruleengine.Row{"visitor_id": item.author, "approved_at": item.approval}
					oldVisible := oldAllowed
					if action.op == ruleengine.OpAdd {
						// The row an add is judged on is the row after today's stamping.
						body := map[string]json.RawMessage{"visitor_id": json.RawMessage(`"forged"`)}
						stampInsertValues(columns, body, InsertActor{OwnerOnSite: oldCall.OwnerOnSite, Hold: hold, StampID: c.VisitorID})
						row["visitor_id"] = ruleengine.Null()
						var author string
						if json.Unmarshal(body["visitor_id"], &author) == nil && author != "" {
							row["visitor_id"] = ruleengine.Text(author)
						}
						if hold {
							row["approved_at"] = ruleengine.Null()
							if oldCall.OwnerOnSite {
								row["approved_at"] = ruleengine.Text("approved")
							}
						}
					} else {
						oldVisible = oldVisible && (filter == "" || item.author.Kind == ruleengine.KindText && item.author.Text == filter)
						oldVisible = oldVisible && (!hold || oldCall.OwnerOnSite || item.approval.Kind != ruleengine.KindNull)
					}
					decided, err := convertRowDecision(p, action.op, c, row)
					if err != nil {
						return err
					}
					if newVisible := newAllowed && decided; oldVisible != newVisible {
						differ(name, action.name, visible(item.kind, oldVisible), visible(item.kind, newVisible))
					}
				}
			}
		}
	}
	return nil
}

// Synthetic schemas for key-value data and tables not yet created include
// both server columns. Existing SQLite tables use their real columns.
func convertColumns(types map[string]string) map[string]string {
	out := map[string]string{}
	for name, typ := range types {
		out[name] = typ
	}
	for _, name := range []string{"visitor_id", "approved_at"} {
		found := false
		for have := range out {
			found = found || strings.EqualFold(have, name)
		}
		if !found {
			out[name] = "TEXT"
		}
	}
	return out
}

func (e *convertExam) compare(schema ruleengine.BindingSchema) ([]ConvertDifference, error) {
	var out []ConvertDifference
	run := func(table string, columns map[string]string, set ruleengine.PolicySet, p *ruleengine.Plan) error {
		return convertCompare(e.x, table, columns, set, p, func(caller, action, old, new string) {
			out = append(out, ConvertDifference{Account: e.res.Account, Site: e.res.SiteName, Resource: e.res.Name, Table: table, Caller: caller, Action: action, Old: old, New: new})
		})
	}
	if e.x.Kind == "kv" {
		err := run("kv", convertColumns(nil), e.def, e.kvPlan)
		return out, err
	}
	real := map[string]bool{}
	for _, t := range e.tables {
		lower := strings.ToLower(t.name)
		real[lower] = true
		set, ok := e.sets[lower]
		if !ok {
			set = e.def
		}
		if err := run(t.name, t.types, set, e.plans[lower]); err != nil {
			return nil, err
		}
	}
	// The default set, and each table set whose table does not exist yet,
	// are compared on the two server columns alone: they apply to tables
	// created later. "(default)" is a name no table has, so the resource's
	// own matrix is the one compared there.
	later := []string{""}
	for name := range e.sets {
		if !real[name] {
			later = append(later, name)
		}
	}
	sort.Strings(later)
	for _, name := range later {
		set, label := e.def, "(default)"
		if name != "" {
			set, label = e.sets[name], name
		}
		table := ruleengine.Table{Name: label, RowKey: "rowid", Columns: []ruleengine.Column{{Name: "visitor_id"}, {Name: "approved_at"}}}
		p, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
		if len(errs) != 0 {
			return nil, fmt.Errorf("converted rules do not compile: %s", errs[0].Message)
		}
		if err := run(label, convertColumns(nil), set, p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (e *convertExam) note(reason string) ConvertPlanNote {
	return ConvertPlanNote{SiteID: e.res.SiteID, Account: e.res.Account, Site: e.res.SiteName, Resource: e.res.Name, Reason: reason}
}

func (e *convertExam) entry() ConvertPlanEntry {
	entry := ConvertPlanEntry{SiteID: e.res.SiteID, Account: e.res.Account, Site: e.res.SiteName, Name: e.res.Name, Kind: e.x.Kind,
		Fingerprint: e.x.Fingerprint(), SchemaDigest: e.digest, Original: e.original,
		Rules: ruleengine.EncodeSet(e.def), Tables: map[string]json.RawMessage{}}
	for name, set := range e.sets {
		entry.Tables[name] = ruleengine.EncodeSet(set)
	}
	entry.SHA256 = entry.digest()
	return entry
}

// DryRun examines every resource in scope and returns the plan. It writes
// nothing: Postgres is only read, and SQLite is opened read-only. accounts
// nil means every account. The error is ErrConvertAttention when the plan
// lists a difference or a resource that needs a person.
func (m *RulesConverter) DryRun(ctx context.Context, accounts []string, out io.Writer) (ConvertPlan, error) {
	run := *m
	run.db = m.DB
	m = &run
	plan := ConvertPlan{Version: convertPlanVersion, Commit: m.Commit, Created: time.Now().UTC().Format(time.RFC3339),
		Resources: []ConvertPlanEntry{}, NeedsPerson: []ConvertPlanNote{}, Differences: []ConvertDifference{}, Notes: []ConvertPlanNote{}}
	for _, handle := range accounts {
		ok, err := repo.AccountHandleExists(ctx, m.DB, handle)
		if err != nil {
			return plan, err
		}
		if !ok {
			return plan, fmt.Errorf("no account has the handle %q", handle)
		}
	}
	scope, err := repo.ConvertScope(ctx, m.DB, accounts)
	if err != nil {
		return plan, err
	}
	var failures []error
	for _, r := range scope {
		e, err := m.examine(ctx, r, true, false)
		if errors.Is(err, sql.ErrNoRows) {
			continue // removed while the dry run was reading
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s %s: %w", r.Account, r.SiteName, r.Name, err))
			fmt.Fprintf(out, "failed %s %s %s: %v\n", r.Account, r.SiteName, r.Name, err)
			continue
		}
		switch {
		case e.x.Rules != nil:
			// Saved as rules while the dry run was reading: out of scope.
		case len(e.needs) != 0:
			for _, reason := range e.needs {
				plan.NeedsPerson = append(plan.NeedsPerson, e.note(reason))
				fmt.Fprintf(out, "needs a person: %s %s %s: %s\n", r.Account, r.SiteName, r.Name, reason)
			}
		case len(e.differences) != 0:
			plan.Differences = append(plan.Differences, e.differences...)
			for _, d := range e.differences {
				fmt.Fprintln(out, d.Line())
			}
		default:
			plan.Resources = append(plan.Resources, e.entry())
			if e.x.Kind == "kv" && e.x.KVStore != "sqlite" {
				reason := "this key-value namespace is still on Postgres; apply skips it until it has been moved"
				plan.Notes = append(plan.Notes, e.note(reason))
				fmt.Fprintf(out, "note: %s %s %s: %s\n", r.Account, r.SiteName, r.Name, reason)
			}
		}
	}
	fmt.Fprintf(out, "planned=%d needs_a_person=%d differences=%d failed=%d\n", len(plan.Resources), len(plan.NeedsPerson), len(plan.Differences), len(failures))
	if len(failures) != 0 {
		return plan, errors.Join(failures...)
	}
	if len(plan.NeedsPerson) != 0 || len(plan.Differences) != 0 {
		return plan, ErrConvertAttention
	}
	return plan, nil
}

func decodeConvertRecord(raw json.RawMessage) (convertRecord, bool) {
	var record convertRecord
	if raw == nil || json.Unmarshal(raw, &record) != nil || record.Version != convertPlanVersion {
		return record, false
	}
	return record, true
}

// Apply converts the plan's resources. It holds the exclusive maintenance
// lock for the whole run, so the install must be stopped. A resource that is
// not exactly what the dry run saw is skipped with nothing recorded.
func (m *RulesConverter) Apply(ctx context.Context, plan ConvertPlan, out io.Writer) error {
	m, done, err := m.lockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer done()
	converted, already, skipped := 0, 0, 0
	var failures []error
	for _, entry := range plan.Resources {
		label := entry.Account + " " + entry.Site + " " + entry.Name
		status, err := m.applyEntry(ctx, entry)
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("%s: %w", label, err))
			fmt.Fprintf(out, "failed %s: %v\n", label, err)
		case status == "converted":
			converted++
			fmt.Fprintf(out, "converted %s\n", label)
		case status == "already converted":
			already++
			fmt.Fprintf(out, "already converted %s\n", label)
		default:
			skipped++
			fmt.Fprintf(out, "skipped %s: %s\n", label, status)
		}
	}
	fmt.Fprintf(out, "converted=%d already_converted=%d skipped=%d failed=%d\n", converted, already, skipped, len(failures))
	if len(failures) != 0 {
		return errors.Join(append([]error{ErrConvertIncomplete}, failures...)...)
	}
	if skipped != 0 {
		return ErrConvertIncomplete
	}
	return nil
}

const convertChanged = "changed since the dry run"

func (m *RulesConverter) applyEntry(ctx context.Context, entry ConvertPlanEntry) (string, error) {
	if entry.SHA256 == "" || entry.digest() != entry.SHA256 {
		return "", fmt.Errorf("%w: the entry does not match its SHA-256", ErrConvertPlan)
	}
	wantSHA, err := convertSetsSHA(entry.Rules, entry.Tables)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrConvertPlan, err)
	}
	r, err := repo.ConvertTarget(ctx, m.db, entry.SiteID, entry.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return "the resource no longer exists", nil
	}
	if err != nil {
		return "", err
	}
	// Everything below re-validates against what is there now, before step 1.
	e, err := m.examine(ctx, r, true, true)
	if err != nil {
		return "", err
	}
	record, recorded := decodeConvertRecord(e.x.ConvertedFrom)
	if e.x.Rules != nil {
		if have, err := resourceSetsSHA(e.x); recorded && err == nil && record.PlanEntrySHA256 == entry.SHA256 && have == record.RulesSHA256 {
			return "already converted", nil
		}
		return convertChanged, nil
	}
	if e.x.Fingerprint() != entry.Fingerprint || !bytes.Equal(convertEncode(e.original), convertEncode(entry.Original)) || e.x.Kind != entry.Kind {
		return convertChanged, nil
	}
	if len(e.needs) != 0 {
		return "needs a person: " + strings.Join(e.needs, "; "), nil
	}
	if len(e.differences) != 0 {
		return "conversion changes answers: " + e.differences[0].Line(), nil
	}
	if e.x.Kind == "kv" && e.x.KVStore != "sqlite" {
		return "this key-value namespace is still on Postgres", nil
	}
	// A rerun after this plan's own additive step sees the schema that step left.
	rerun := recorded && record.PlanEntrySHA256 == entry.SHA256 && record.SchemaDigest == entry.SchemaDigest
	if e.digest != entry.SchemaDigest && !(rerun && e.digest == record.PreparedSchemaDigest) {
		return convertChanged, nil
	}
	current := e.entry()
	if !bytes.Equal(current.Rules, entry.Rules) || len(current.Tables) != len(entry.Tables) {
		return convertChanged, nil
	}
	for name, set := range current.Tables {
		if planned, ok := entry.Tables[name]; !ok || !bytes.Equal(set, planned) {
			return convertChanged, nil
		}
	}

	// Step 1: record what the resource was. Nothing a request reads changes.
	if !rerun {
		record = convertRecord{Version: convertPlanVersion, Original: entry.Original, Commit: m.Commit, RulesSHA256: wantSHA,
			PlanEntrySHA256: entry.SHA256, SchemaDigest: entry.SchemaDigest, PreparedSchemaDigest: e.prepared}
	}
	ok, err := repo.RecordConvertedFrom(ctx, m.db, entry.SiteID, entry.Name, convertEncode(record))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", repo.ErrConvertRowCount
	}
	if err = m.at("recorded"); err != nil {
		return "", err
	}

	// Step 2: the additive preparation a rules save does. Never a cleanup.
	if e.x.Kind == "sqlite" {
		path := m.sqlitePath(r)
		if info, statErr := os.Stat(path); statErr == nil {
			size := info.Size()
			_, err = runtime.PrepareMatrixDatabase(ctx, path, 0, size+convertPrepareHeadroom, size, size, false, false, true,
				e.plans.OwnTables(), e.plans.Holds, func(error) {}, func() {}, func(map[string]bool) {})
			if err != nil {
				return "", fmt.Errorf("preparing the database: %w", err)
			}
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		if err = m.at("prepared"); err != nil {
			return "", err
		}
		// As a rules save does: the sets must compile against the real columns.
		if _, _, err = m.inspect(ctx, r, &e.def, e.sets, PlatformSchema(m.SpamCheckAvailable), false, true); err != nil {
			return "", fmt.Errorf("the prepared database does not take the converted rules: %w", err)
		}
	} else if err = m.at("prepared"); err != nil {
		return "", err
	}

	// Step 3: the rules and the fill, in one transaction.
	deny := rules.AccessMatrix{Read: rules.WhoNobody, Add: rules.WhoNobody, Edit: rules.WhoNobody, Delete: rules.WhoNobody}
	tables := map[string]rules.TableRule{}
	for name, set := range entry.Tables {
		tables[name] = rules.TableRule{Rules: set, Matrix: deny, PresetBase: "rules"}
	}
	if err = repo.ConvertToRules(ctx, m.db, entry.SiteID, entry.Name, entry.Rules, tables, func() error { return m.at("before-commit") }); err != nil {
		return "", err
	}
	return "converted", m.at("converted")
}

// ConvertTarget names one resource for Revert.
type ConvertTarget struct{ SiteID, Resource string }

// Revert puts converted resources back. It holds the exclusive maintenance
// lock. A resource whose rules are not the ones apply wrote is left alone.
// The columns apply added to SQLite stay; presets treat them as they do today.
func (m *RulesConverter) Revert(ctx context.Context, targets []ConvertTarget, out io.Writer) error {
	m, done, err := m.lockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer done()
	reverted, skipped := 0, 0
	var failures []error
	for _, t := range targets {
		label := t.SiteID + " " + t.Resource
		status, err := m.revertOne(ctx, t)
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("%s: %w", label, err))
			fmt.Fprintf(out, "failed %s: %v\n", label, err)
		case status == "reverted":
			reverted++
			fmt.Fprintf(out, "reverted %s\n", label)
		default:
			skipped++
			fmt.Fprintf(out, "skipped %s: %s\n", label, status)
		}
	}
	fmt.Fprintf(out, "reverted=%d skipped=%d failed=%d\n", reverted, skipped, len(failures))
	if len(failures) != 0 {
		return errors.Join(append([]error{ErrConvertIncomplete}, failures...)...)
	}
	if skipped != 0 {
		return ErrConvertIncomplete
	}
	return nil
}

func (m *RulesConverter) revertOne(ctx context.Context, t ConvertTarget) (string, error) {
	x, err := repo.LoadResource(ctx, m.db, t.SiteID, t.Resource)
	if errors.Is(err, sql.ErrNoRows) {
		return "the resource no longer exists", nil
	}
	if err != nil {
		return "", err
	}
	if x.ConvertedFrom == nil {
		return "it was not converted by migrate-rules", nil
	}
	record, ok := decodeConvertRecord(x.ConvertedFrom)
	if !ok {
		return "", fmt.Errorf("the record of its original configuration cannot be read")
	}
	if have, err := resourceSetsSHA(x); err != nil || have != record.RulesSHA256 {
		return "rules were changed after conversion", nil
	}
	if err = repo.RevertFromRules(ctx, m.db, t.SiteID, t.Resource, record.Original, func() error { return m.at("before-revert-commit") }); err != nil {
		return "", err
	}
	return "reverted", nil
}
