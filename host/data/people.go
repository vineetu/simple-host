package data

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	accountapp "github.com/vsriram/simple-host/host/access"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/host/data/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// Person is one entry in the owner's pick list: a reference (visitor_id,
// usable as ResolveOwnerFor's ref and, through storage_visitor_emails, as
// the opaque reference an MCP caller holds) with the email they signed in
// with when known, and why they are listed.
type Person struct {
	VisitorID, Email string
	// NamedViewer: one of this site's named viewers (set_site_access,
	// grant_site_viewer).
	NamedViewer bool
	// HasData: at least one KV key, stored file, or SQLite row of theirs.
	HasData bool
	// SignedIn: a visitor_id is known for them. false only for a named
	// viewer who has never signed in on this site: listed (the owner
	// already named them) but not yet a usable ResolveOwnerFor target.
	SignedIn bool
}

// SitePeople lists the site's people for the owner's pick list: visitors
// who have saved data here, plus named viewers, and only them — never
// everyone who ever merely signed in (the privacy rule). Owner only (API
// key, connector token, or signed in on the site's own address).
func (s *Service) SitePeople(ctx context.Context, c Call) ([]Person, error) {
	if !c.Owner && !c.OwnerOnSite {
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	writers, err := s.siteDataWriters(ctx, c)
	if err != nil {
		return nil, err
	}
	viewers, byEmail, err := s.namedViewers(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make([]Person, 0, len(writers)+len(viewers)+len(byEmail))
	index := map[string]int{}
	for _, id := range writers {
		index[id] = len(out)
		out = append(out, Person{VisitorID: id, SignedIn: true, HasData: true})
	}
	for _, id := range viewers {
		i, ok := index[id]
		if !ok {
			i, index[id] = len(out), len(out)
			out = append(out, Person{VisitorID: id, SignedIn: true})
		}
		out[i].NamedViewer = true
	}
	for i := range out {
		email, err := accountapp.VisitorEmail(ctx, s.db, out[i].VisitorID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
		out[i].Email = email
	}
	return append(out, byEmail...), nil
}

// siteDataWriters returns the visitors with a KV key, stored file or SQLite
// row here. KV and file writer_id are server-stamped and already
// trustworthy, but a SQLite row's visitor_id can be whatever the owner's raw
// storage_sql_execute wrote, including another account's id entirely.
// Every candidate, from every source, is confirmed
// against the server's own site_visitor_signins record before it is listed
// or its email resolved — never trust a value read back out of a table.
func (s *Service) siteDataWriters(ctx context.Context, c Call) ([]string, error) {
	kvIDs, err := s.sqliteKVVisitors(ctx, c)
	if err != nil {
		return nil, err
	}
	fileIDs, err := repository.DistinctFileWriters(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	rowIDs, err := s.sqliteRowVisitors(ctx, c)
	if err != nil {
		return nil, err
	}
	hasData := map[string]bool{}
	for _, id := range slices.Concat(kvIDs, fileIDs, rowIDs) {
		hasData[id] = true
	}
	candidates := slices.Collect(maps.Keys(hasData))
	signedIn, err := accountsrepo.SignedInAmong(ctx, s.db, c.SiteID, candidates)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	return slices.DeleteFunc(candidates, func(id string) bool { return !signedIn[id] }), nil
}

// namedViewers returns the visitor ids of the site's named viewers, and,
// listed by email only, the viewers no single current account matches.
func (s *Service) namedViewers(ctx context.Context, c Call) (ids []string, byEmail []Person, _ error) {
	viewers, err := accountsrepo.ListSiteViewers(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	for _, v := range viewers {
		id, found, err := "", false, error(nil)
		if v.SignedInAt != nil {
			id, found, err = s.resolveSignedInVisitorByEmail(ctx, c.SiteID, strings.ToLower(v.Email))
		}
		switch {
		case errors.Is(err, errAmbiguousVisitorEmail):
			// More than one signed-in account resolves to this email: never
			// guess which one.
		case err != nil:
			return nil, nil, &Error{Code: "internal_error", Message: "internal server error"}
		case found:
			ids = append(ids, id)
			continue
		}
		// Never signed in, ambiguous, or no current account matches the
		// email (it changed since): listed by email only.
		byEmail = append(byEmail, Person{Email: v.Email, NamedViewer: true})
	}
	return ids, byEmail, nil
}

// sqliteRowVisitors scans every SQLite resource's author-tracked tables
// (as declared by its rules) for distinct, nonempty visitor_id
// values. Opened read-only, directly by site runtime path (not through
// s.deps.SQLitePath, which is bound to one resource): the pick list spans
// every resource on the site.
func (s *Service) sqliteRowVisitors(ctx context.Context, c Call) ([]string, error) {
	resources, err := s.ListResourceRecords(ctx, c.SiteID)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range resources {
		if r.Kind != "sqlite" {
			continue
		}
		path := filepath.Join(c.RuntimeDir, "sqlite", r.Name+".sqlite")
		var ids []string
		err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
			conn, done, err := runtime.OpenTableConn(ctx, path, false, s.deps.SQLiteTimeout, s.deps.SQLiteMaxMemory, nil)
			if err != nil {
				return err
			}
			defer closeSQLite(func() { defer done(); s.sqliteStage("close") })
			s.sqliteStage("after_open")
			ids, err = s.distinctRowVisitors(conn, r)
			return err
		})
		if err != nil {
			continue // a busy or briefly unreadable resource does not fail the whole list
		}
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func (s *Service) distinctRowVisitors(conn *sqlite3.Conn, r model.Resource) ([]string, error) {
	tables, err := runtime.SQLTables(conn)
	if err != nil {
		return nil, err
	}
	if err := conn.SetAuthorizer(nil); err != nil {
		return nil, err
	}
	var out []string
	for table := range tables {
		c := Call{Resource: r}
		if err := s.compileBindingRules(&c, table); err != nil || !c.rulePlan.AuthorTracked() {
			continue
		}
		cols, err := runtime.TableColumns(conn, table)
		author := declaredColumn(cols, "visitor_id")
		if err != nil || cols[author] != "TEXT" {
			continue
		}
		stmt, _, err := conn.Prepare(`SELECT DISTINCT visitor_id FROM ` + runtime.Quote(table) + ` WHERE visitor_id IS NOT NULL AND visitor_id <> ''`)
		if err != nil {
			continue
		}
		defer stmt.Close()
		for stmt.Step() {
			out = append(out, stmt.ColumnText(0))
		}
		stmt.Close()
	}
	return out, nil
}

func (s *Service) sqliteKVVisitors(ctx context.Context, c Call) ([]string, error) {
	names, err := repository.KVNamespaces(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, name := range names {
		err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
			conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(c.RuntimeDir, name), false, false)
			if err != nil {
				return err
			}
			defer closeSQLite(done)
			writers, err := runtime.RuntimeKVWriters(conn)
			if err == nil {
				ids = append(ids, writers...)
			}
			return err
		})
		if err != nil {
			continue // Same best-effort policy as SQLite row authors.
		}
	}
	return ids, nil
}
