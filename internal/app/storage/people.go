package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repoSites "github.com/vsriram/simple-host/internal/repository/sites"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
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
	byID := map[string]*Person{}
	order := []string{}
	get := func(id string) *Person {
		p, ok := byID[id]
		if !ok {
			p = &Person{VisitorID: id, SignedIn: true}
			byID[id] = p
			order = append(order, id)
		}
		return p
	}

	kvIDs, err := repository.DistinctKVWriters(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	fileIDs, err := repository.DistinctFileWriters(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	rowIDs, err := s.sqliteRowVisitors(ctx, c)
	if err != nil {
		return nil, err
	}
	// hasData's keys are raw candidate ids: KV and file writer_id are
	// server-stamped and already trustworthy, but a SQLite row's
	// visitor_id can be whatever the owner's raw storage_sql_execute
	// wrote, including another account's id entirely (review fix,
	// SIM-358). Every candidate, from every source, is confirmed against
	// the server's own site_visitor_signins record before it is listed or
	// its email resolved — never trust a value read back out of a table.
	hasData := map[string]bool{}
	for _, id := range kvIDs {
		hasData[id] = true
	}
	for _, id := range fileIDs {
		hasData[id] = true
	}
	for _, id := range rowIDs {
		hasData[id] = true
	}
	candidates := make([]string, 0, len(hasData))
	for id := range hasData {
		candidates = append(candidates, id)
	}
	signedIn, err := repository.SignedInAmong(ctx, s.db, c.SiteID, candidates)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	for id := range hasData {
		if !signedIn[id] {
			continue
		}
		get(id).HasData = true
	}

	viewers, err := repoSites.ListSiteViewers(ctx, s.db, c.SiteID)
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	var unresolved []Person
	for _, v := range viewers {
		if v.SignedInAt == nil {
			unresolved = append(unresolved, Person{Email: v.Email, NamedViewer: true})
			continue
		}
		id, found, err := s.resolveSignedInVisitorByEmail(ctx, c.SiteID, strings.ToLower(v.Email))
		if errors.Is(err, errAmbiguousVisitorEmail) {
			// More than one signed-in account currently resolves to this
			// email: list the named viewer by email, never guess which one.
			unresolved = append(unresolved, Person{Email: v.Email, NamedViewer: true})
			continue
		}
		if err != nil {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
		if !found {
			// The viewer's first-signed-in timestamp exists, but no current
			// account matches this email (it changed since): list by email
			// only, the same as never having signed in.
			unresolved = append(unresolved, Person{Email: v.Email, NamedViewer: true})
			continue
		}
		get(id).NamedViewer = true
	}

	out := make([]Person, 0, len(order)+len(unresolved))
	for _, id := range order {
		p := byID[id]
		if p.Email == "" {
			email, err := s.deps.VisitorEmail(ctx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, &Error{Code: "internal_error", Message: "internal server error"}
			}
			p.Email = email
		}
		out = append(out, *p)
	}
	out = append(out, unresolved...)
	return out, nil
}

// sqliteRowVisitors scans every SQLite resource's own tables (those whose
// matrix uses own, or lets visitors add) for distinct, nonempty visitor_id
// values. Opened read-only, directly by site runtime path (not through
// s.deps.SQLitePath, which is bound to one resource): the pick list spans
// every resource on the site.
func (s *Service) sqliteRowVisitors(ctx context.Context, c Call) ([]string, error) {
	resources, err := s.ListResourceRecords(ctx, ResourceListInput{SiteID: c.SiteID})
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
		conn, done, err := runtime.OpenTableConn(ctx, path, false, s.deps.SQLiteGate, s.deps.SQLiteTimeout, s.deps.SQLiteMaxMemory, nil)
		if err != nil {
			continue // a resource busy or briefly unreadable does not fail the whole list
		}
		ids, err := distinctRowVisitors(conn, r)
		done()
		if err != nil {
			continue
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

func distinctRowVisitors(conn *sqlite3.Conn, r rules.Resource) ([]string, error) {
	tables, err := runtime.SQLTables(conn)
	if err != nil {
		return nil, err
	}
	if err := conn.SetAuthorizer(nil); err != nil {
		return nil, err
	}
	var out []string
	for table := range tables {
		if !r.TableMatrix(table).RecordsAuthor() {
			continue
		}
		cols, err := runtime.TableColumns(conn, table)
		if err != nil || cols["visitor_id"] != "TEXT" {
			continue
		}
		stmt, _, err := conn.Prepare(`SELECT DISTINCT visitor_id FROM ` + runtime.Quote(table) + ` WHERE visitor_id IS NOT NULL AND visitor_id <> ''`)
		if err != nil {
			continue
		}
		for stmt.Step() {
			out = append(out, stmt.ColumnText(0))
		}
		stmt.Close()
	}
	return out, nil
}
