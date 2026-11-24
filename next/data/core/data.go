// Package data (data/core) owns saved data: one SQLite file per site
// namespace, holding the owner's tables plus _values and _files. It opens
// that file sandboxed behind exactly two guards (a size cap per site and a
// time limit per query), compiles the namespace's rules, checks Turnstile and
// records data-change events.
package data

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"

	"simplehost/core/db"
	"simplehost/core/limits"
	"simplehost/core/web"
	"simplehost/data/core/rules"
	sites "simplehost/sites/core"
)

// Schema is the data module's table (namespaces). Applied after sites.Schema.
//
//go:embed schema.sql
var Schema string

// Data opens namespaces and checks the rules around them.
type Data struct {
	DB *sql.DB
	// Dir is the data folder: Dir/sites/<site id>/data/<namespace>.sqlite.
	Dir string
	// TurnstileURL is Cloudflare's siteverify address (the fake in tests).
	TurnstileURL string
	Client       *http.Client
}

// NS is one open namespace. Close it when the request is done.
type NS struct {
	Site  *sites.Site
	Name  string
	Rules map[string]json.RawMessage // table, "_values" or "_files" -> rule set
	// TurnstileKey and TurnstileSecret are the namespace's spam check keys
	// ("" = none).
	TurnstileKey, TurnstileSecret string
	db                            *sql.DB
}

var nsName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidName reports whether name may name a namespace.
func ValidName(name string) bool { return nsName.MatchString(name) }

// BuiltIn makes the tables every namespace file starts with: named values
// and file records.
const BuiltIn = `
CREATE TABLE IF NOT EXISTS _values (key TEXT PRIMARY KEY, value TEXT NOT NULL, author TEXT, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS _files (name TEXT PRIMARY KEY, size INTEGER NOT NULL, type TEXT NOT NULL DEFAULT '', author TEXT, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`

func (d *Data) path(siteID, name string) string {
	return filepath.Join(d.Dir, "sites", siteID, "data", name+".sqlite")
}

// Create makes a namespace on a site, with its empty file.
func (d *Data) Create(ctx context.Context, site *sites.Site, name string) error {
	if !nsName.MatchString(name) {
		return web.Refuse(http.StatusBadRequest, "bad_namespace", "Name the namespace with lower-case letters, digits, _ or -.")
	}
	if err := os.MkdirAll(filepath.Dir(d.path(site.ID, name)), 0o755); err != nil {
		return err
	}
	conn, err := driver.Open("file:" + d.path(site.ID, name))
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, BuiltIn); err != nil {
		return err
	}
	_, err = d.DB.ExecContext(ctx, `INSERT INTO namespaces (site_id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, site.ID, name)
	return err
}

// Sizes is the bytes each namespace of a site uses on disk.
func (d *Data) Sizes(siteID string) (map[string]int64, int64) {
	out, total := map[string]int64{}, int64(0)
	files, _ := filepath.Glob(filepath.Join(d.Dir, "sites", siteID, "data", "*.sqlite"))
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			out[strings.TrimSuffix(filepath.Base(f), ".sqlite")] = info.Size()
			total += info.Size()
		}
	}
	return out, total
}

// safePragmas are the pragmas SQL may use (query_only: the driver itself
// uses it); every other pragma,
// ATTACH and DETACH are refused (the sandbox).
var safePragmas = map[string]bool{"table_info": true, "table_xinfo": true, "table_list": true, "index_list": true,
	"index_info": true, "foreign_key_list": true, "page_count": true, "page_size": true, "freelist_count": true, "query_only": true}

// Open opens a site's namespace for this request. The file may grow only to
// the site's data cap (minus its other namespaces), unless the caller is the
// operator.
func (d *Data) Open(r *http.Request, site *sites.Site, name string) (*NS, error) {
	ns := &NS{Site: site, Name: name}
	var rulesJSON []byte
	err := d.DB.QueryRowContext(r.Context(), `SELECT rules, turnstile_key, turnstile_secret FROM namespaces WHERE site_id = $1 AND name = $2`,
		site.ID, name).Scan(&rulesJSON, &ns.TurnstileKey, &ns.TurnstileSecret)
	if err == sql.ErrNoRows {
		return nil, web.Refuse(http.StatusNotFound, "no_namespace", "This site has no saved data named "+name+". The owner creates it first.")
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rulesJSON, &ns.Rules); err != nil {
		return nil, err
	}
	sizes, total := d.Sizes(site.ID)
	maxPages := (limits.DataBytes.N - (total - sizes[name])) / 4096
	if web.CallerOf(r).Operator {
		maxPages = 1 << 30
	}
	ns.db, err = driver.Open("file:"+d.path(site.ID, name), func(c *sqlite3.Conn) error {
		if err := c.Exec("PRAGMA busy_timeout = 2000; PRAGMA page_size = 4096; PRAGMA max_page_count = " + strconv.FormatInt(max(maxPages, 1), 10)); err != nil {
			return err
		}
		return c.SetAuthorizer(func(action sqlite3.AuthorizerActionCode, a, _, _, _ string) sqlite3.AuthorizerReturnCode {
			if action == sqlite3.AUTH_ATTACH || action == sqlite3.AUTH_DETACH || action == sqlite3.AUTH_PRAGMA && !safePragmas[strings.ToLower(a)] {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		})
	})
	if err != nil {
		return nil, err
	}
	ns.db.SetMaxOpenConns(1)
	return ns, nil
}

// Close releases the namespace's file.
func (n *NS) Close() error { return n.db.Close() }

// Exec runs one writing statement under the two guards.
func (n *NS) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.QueryTime.Per)
	defer cancel()
	res, err := n.db.ExecContext(ctx, query, args...)
	return res, guard(err)
}

// Rows is a query's answer: column names and values in order.
type Rows struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Query runs one reading statement under the two guards and returns every
// row it gives.
func (n *NS) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.QueryTime.Per)
	defer cancel()
	var out Rows
	rows, err := n.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, guard(err)
	}
	defer rows.Close()
	if out.Columns, err = rows.Columns(); err != nil {
		return out, err
	}
	for rows.Next() {
		vals := make([]any, len(out.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, guard(err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out.Rows = append(out.Rows, vals)
	}
	return out, guard(rows.Err())
}

// guard names the guard an error hit, or reports the SQL mistake plainly.
func guard(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sqlite3.INTERRUPT):
		return web.Refuse(http.StatusRequestTimeout, "limit_"+limits.QueryTime.Name, limits.QueryTime.Fix)
	case errors.Is(err, sqlite3.FULL):
		return web.Refuse(http.StatusRequestEntityTooLarge, "limit_"+limits.DataBytes.Name, limits.DataBytes.Fix)
	case errors.Is(err, sqlite3.ERROR) || errors.Is(err, sqlite3.CONSTRAINT) || errors.Is(err, sqlite3.AUTH) || errors.Is(err, sqlite3.MISMATCH) || errors.Is(err, sqlite3.RANGE):
		return web.Refuse(http.StatusBadRequest, "sql_error", err.Error())
	}
	return err
}

// Plan compiles the namespace's rules for one table ("_values" and "_files"
// included) against its columns. A nil plan means no rules: only the owner.
// Check web.CallerOf(r) with sites.Manage first: the owner passes every rule.
func (n *NS) Plan(ctx context.Context, table string) (*rules.Plan, error) {
	raw, ok := n.Rules[table]
	if !ok {
		return nil, nil
	}
	set, errs := rules.DecodeSet(raw)
	if len(errs) > 0 {
		return nil, ruleErr(errs)
	}
	cols, err := n.Query(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	t := rules.Table{Name: table, RowKey: "rowid"}
	for _, c := range cols.Rows {
		t.Columns = append(t.Columns, rules.Column{Name: c[0].(string)})
	}
	plan, errs := rules.Compile(set, t, RuleSchema(n.TurnstileSecret != ""), rules.DefaultLimits())
	if len(errs) > 0 {
		return nil, ruleErr(errs)
	}
	return plan, nil
}

func ruleErr(errs []rules.RuleError) error {
	msg := make([]string, len(errs))
	for i, e := range errs {
		msg[i] = e.Error()
	}
	return web.Refuse(http.StatusBadRequest, "bad_rules", strings.Join(msg, "; "))
}

// RuleSchema is what rules may name about the caller: auth.id (the person's
// opaque id, also written as a row's author), auth.signed_in, auth.role
// ("owner", "visitor" or "anon"), auth.ai and auth.teams (kept so existing
// rules still compile), and auth.checked when the namespace has a spam check.
func RuleSchema(spamCheck bool) rules.BindingSchema {
	s := rules.BindingSchema{
		"auth.id":        {Type: rules.TypeText, Nullable: true},
		"auth.signed_in": {Type: rules.TypeBool},
		"auth.role":      {Type: rules.TypeText},
		"auth.ai":        {Type: rules.TypeBool},
		"auth.teams":     {Type: rules.TypeTextList},
	}
	if spamCheck {
		s["auth.checked"] = rules.Binding{Type: rules.TypeBool, AddCheckOnly: true}
	}
	return s
}

// Bindings are the rule values for this request's caller. owner is the
// answer of sites.Manage; checked is whether a spam check passed. A person's
// AI app is that person, so auth.ai is always false.
func Bindings(r *http.Request, owner, checked bool) rules.Values {
	c := web.CallerOf(r)
	id, role := rules.Null(), "anon"
	if c.ID != "" {
		id, role = rules.Text(c.ID), "visitor"
	}
	if owner {
		role = "owner"
	}
	return rules.Values{"auth.id": id, "auth.signed_in": rules.Bool(c.ID != ""), "auth.role": rules.Text(role),
		"auth.ai": rules.Bool(false), "auth.teams": rules.List(), "auth.checked": rules.Bool(checked)}
}

// Turnstile asks Cloudflare whether a spam-check token passed.
func (d *Data) Turnstile(r *http.Request, secret, token string) error {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	form := url.Values{"secret": {secret}, "response": {token}, "remoteip": {web.ClientIP(r)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TurnstileURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var out struct{ Success bool }
	if json.NewDecoder(res.Body).Decode(&out) != nil || !out.Success {
		return web.Refuse(http.StatusForbidden, "spam_check_failed", "The spam check did not pass. Reload the page and try again.")
	}
	return nil
}

// Changed records a data change as an event when the site's owner asked to
// be told about this target (a table, "_values" or "_files") of the namespace.
func (d *Data) Changed(ctx context.Context, site *sites.Site, ns, target, action string) error {
	var watched bool
	err := d.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM notify_rules WHERE site_id = $1 AND kind = 'data' AND namespace = $2 AND target = $3)`,
		site.ID, ns, target).Scan(&watched)
	if err != nil || !watched {
		return err
	}
	return db.Event(ctx, d.DB, "data", site.OwnerID, site.ID, map[string]string{"site": site.Name, "namespace": ns, "target": target, "action": action})
}
