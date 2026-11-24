// Package sites (sites/core) owns sites, their versions and addresses. It
// holds the one host -> site lookup, the one access decision for a visit,
// and the atomic version write.
package sites

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	access "simplehost/access/core"
	"simplehost/core/db"
	"simplehost/core/limits"
	"simplehost/core/web"
)

// Schema is the sites module's tables. Applied after access.Schema.
//
//go:embed schema.sql
var Schema string

// Sites reads and writes the sites tables and the version folders.
type Sites struct {
	DB     *sql.DB
	People *access.People
	// Dir is the data folder: versions live in Dir/sites/<site id>/v<n>/.
	Dir string
	// AppDomain is the service's own host and the older address family
	// (simple-host.app). SiteDomain is the flat family (simple-host.site).
	AppDomain, SiteDomain string
	keys                  passcodeKeys

	mu    sync.Mutex
	cache map[string]Place
}

// New makes the sites core. passcodeKey is PASSCODE_ENC_KEY (32 bytes,
// base64); empty means passcodes cannot be set.
func New(conn *sql.DB, people *access.People, dir, appDomain, siteDomain, passcodeKey string) (*Sites, error) {
	s := &Sites{DB: conn, People: people, Dir: dir, AppDomain: appDomain, SiteDomain: siteDomain, cache: map[string]Place{}}
	return s, s.keys.set(passcodeKey)
}

// Site is one site with what a visit needs to know about its owner.
type Site struct {
	ID          string
	OwnerID     string
	OwnerHandle string
	// AppShape: the owner's sites answer at <site>.<handle>.simple-host.app.
	AppShape       bool
	OwnerSuspended bool
	Name           string
	LiveVersion    int
	Listed         bool
	Offline        bool
	TakenDown      bool
	Access         string // "open", "passcode" or "viewers"
	PasscodeEnc    []byte
	PasscodeGen    int
}

// Place is what a host name answers for: a site, or the public page of a
// person or team (Owner set, Site nil). Redirect is set on the www or bare
// partner of an own domain: the connected host, which HTTPS visits go to.
type Place struct {
	Site     *Site
	Owner    *access.Person
	Redirect string
}

const siteColumns = `s.id, s.owner_id, COALESCE(n.name, ''), COALESCE(a.app_shape, false), o.suspended_at IS NOT NULL,
	s.name, s.live_version, s.listed, s.offline, s.taken_down_at IS NOT NULL, s.access, s.passcode_enc, s.passcode_gen
	FROM sites s JOIN people o ON o.id = s.owner_id
	LEFT JOIN names n ON n.person_id = o.id AND n.kind = 'handle' AND n.retired_at IS NULL
	LEFT JOIN owner_addresses a ON a.owner_id = o.id
	WHERE s.deleted_at IS NULL AND `

func (s *Sites) one(ctx context.Context, where string, args ...any) (*Site, error) {
	x := &Site{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+siteColumns+where, args...).Scan(&x.ID, &x.OwnerID, &x.OwnerHandle, &x.AppShape,
		&x.OwnerSuspended, &x.Name, &x.LiveVersion, &x.Listed, &x.Offline, &x.TakenDown, &x.Access, &x.PasscodeEnc, &x.PasscodeGen)
	if err == sql.ErrNoRows {
		return nil, web.NotFound
	}
	return x, err
}

// Get reads a live (not deleted) site by owner and name.
func (s *Sites) Get(ctx context.Context, ownerID, name string) (*Site, error) {
	return s.one(ctx, `s.owner_id = $1 AND s.name = $2`, ownerID, name)
}

// Count is how many live (not deleted) sites an owner has.
func (s *Sites) Count(ctx context.Context, ownerID string) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE owner_id = $1 AND deleted_at IS NULL`, ownerID).Scan(&n)
	return n, err
}

// ByID reads a live site by id.
func (s *Sites) ByID(ctx context.Context, id string) (*Site, error) {
	return s.one(ctx, `s.id = $1`, id)
}

// Lookup turns a host name into a site or a public page. It knows four
// shapes: <site>--<handle>.<SiteDomain>, <site>.<handle>.<AppDomain>, a
// connected own domain or short name, and <label>.<shared domain>. Anything
// else is not found. Answers are kept in memory until Forget.
func (s *Sites) Lookup(ctx context.Context, host string) (Place, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	s.mu.Lock()
	p, ok := s.cache[host]
	s.mu.Unlock()
	if ok {
		return p, nil
	}
	p, err := s.lookup(ctx, host)
	if err != nil {
		return p, err
	}
	s.mu.Lock()
	s.cache[host] = p
	s.mu.Unlock()
	return p, nil
}

// Forget drops every remembered lookup. Call it after changing anything a
// site's address or visit depends on: sites, names, hostnames,
// owner_addresses, or an account's suspension.
func (s *Sites) Forget() {
	s.mu.Lock()
	s.cache = map[string]Place{}
	s.mu.Unlock()
}

func (s *Sites) lookup(ctx context.Context, host string) (Place, error) {
	label, rest, _ := strings.Cut(host, ".")
	owner := func(handle string, appShape bool) (*access.Person, error) {
		o, err := s.People.ByHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		var shape bool
		err = s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT app_shape FROM owner_addresses WHERE owner_id = $1), false)`, o.ID).Scan(&shape)
		if err == nil && shape != appShape {
			err = web.NotFound
		}
		return &o, err
	}
	site := func(o *access.Person, err error, name string) (Place, error) {
		if err != nil {
			return Place{}, err
		}
		x, err := s.Get(ctx, o.ID, name)
		return Place{Site: x}, err
	}
	switch {
	case rest == s.SiteDomain:
		if name, handle, ok := strings.Cut(label, "--"); ok {
			o, err := owner(handle, false)
			return site(o, err, name)
		}
		o, err := owner(label, false)
		return Place{Owner: o}, err
	case rest == s.AppDomain:
		var kind string
		var siteID sql.NullString
		err := s.DB.QueryRowContext(ctx, `SELECT kind, site_id FROM names WHERE name = $1 AND retired_at IS NULL`, label).Scan(&kind, &siteID)
		if err == sql.ErrNoRows || (kind == "short" && !siteID.Valid) {
			return Place{}, web.NotFound
		}
		if err != nil {
			return Place{}, err
		}
		if kind == "short" {
			x, err := s.ByID(ctx, siteID.String)
			return Place{Site: x}, err
		}
		o, err := owner(label, true)
		return Place{Owner: o}, err
	case strings.HasSuffix(rest, "."+s.AppDomain) && !strings.Contains(strings.TrimSuffix(rest, "."+s.AppDomain), "."):
		o, err := owner(strings.TrimSuffix(rest, "."+s.AppDomain), true)
		return site(o, err, label)
	}
	var siteID sql.NullString
	var own string
	err := s.DB.QueryRowContext(ctx, `SELECT host, site_id FROM hostnames WHERE host IN ($1, $2) AND kind = 'own' AND proven_at IS NOT NULL
		ORDER BY host = $1 DESC LIMIT 1`, host, Partner(host)).Scan(&own, &siteID)
	if err == nil {
		x, err := s.ByID(ctx, siteID.String)
		p := Place{Site: x}
		if own != host {
			p.Redirect = own
		}
		return p, err
	}
	if err != sql.ErrNoRows {
		return Place{}, err
	}
	var ownerID, prefix string
	err = s.DB.QueryRowContext(ctx, `SELECT owner_id, prefix FROM hostnames WHERE host = $1 AND kind = 'shared' AND proven_at IS NOT NULL`, rest).Scan(&ownerID, &prefix)
	if err == sql.ErrNoRows {
		return Place{}, web.NotFound
	}
	if err != nil {
		return Place{}, err
	}
	x, err := s.Get(ctx, ownerID, prefix+label)
	return Place{Site: x}, err
}

type placeKey struct{}

// WithPlace returns r carrying the place its host answered for.
func WithPlace(r *http.Request, p Place) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), placeKey{}, p))
}

// PlaceOf is the place the request's host answered for (zero on the platform).
func PlaceOf(r *http.Request) Place {
	p, _ := r.Context().Value(placeKey{}).(Place)
	return p
}

// Of is the site a request is about. On a site address it is that site. On
// the platform it is the {site} path value, owned by the ?owner= handle (a
// team) or else by the caller.
func (s *Sites) Of(r *http.Request) (*Site, error) {
	if p := PlaceOf(r); p.Site != nil {
		return p.Site, nil
	}
	ownerID := web.CallerOf(r).ID
	if h := r.URL.Query().Get("owner"); h != "" {
		o, err := s.People.ByHandle(r.Context(), h)
		if err != nil {
			return nil, err
		}
		ownerID = o.ID
	}
	if ownerID == "" {
		return nil, web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as Authorization: Bearer <key>.")
	}
	return s.Get(r.Context(), ownerID, r.PathValue("site"))
}

// Manage refuses unless the caller may manage the site: its owner, a member
// of the owning team, or the operator.
func (s *Sites) Manage(r *http.Request, site *Site) error {
	ok, err := s.People.ActsFor(r.Context(), web.CallerOf(r), site.OwnerID)
	if err != nil || ok {
		return err
	}
	if web.CallerOf(r).ID == "" {
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as Authorization: Bearer <key>.")
	}
	return web.Refuse(http.StatusForbidden, "not_yours", "Only the site's owner or their team can do this.")
}

// Decision is the answer of the one access decision for a visit.
type Decision string

const (
	Open       Decision = "open"
	TakenDown  Decision = "taken_down"
	Offline    Decision = "offline"
	NeedCode   Decision = "passcode"
	NeedSignIn Decision = "sign_in"
	AskAccess  Decision = "ask_access"
)

// Visit is the one access decision, before any file or data, the same on
// every address: taken down, offline, passcode, named viewers, open. An
// error (the database is down) must be shown as an error, never the page.
func (s *Sites) Visit(r *http.Request, site *Site) (Decision, error) {
	switch {
	case site.TakenDown || site.OwnerSuspended:
		return TakenDown, nil
	case site.Offline:
		return Offline, nil
	case site.Access == "passcode" && !s.Unlocked(r, site):
		return NeedCode, nil
	case site.Access != "viewers":
		return Open, nil
	}
	c := web.CallerOf(r)
	if c.ID == "" {
		return NeedSignIn, nil
	}
	if ok, err := s.People.ActsFor(r.Context(), c, site.OwnerID); err != nil || ok {
		return Open, err
	}
	var listed bool
	err := s.DB.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM viewers WHERE site_id = $1 AND person_id = $2)`, site.ID, c.ID).Scan(&listed)
	if err != nil || listed {
		return Open, err
	}
	return AskAccess, nil
}

// Err is the decision as a refusal for data and API routes (nil when Open).
func (d Decision) Err() error {
	fix := map[Decision]string{
		TakenDown:  "This site was taken down by the operator.",
		Offline:    "The owner has taken this site offline.",
		NeedCode:   "Unlock the site with its passcode first.",
		NeedSignIn: "Sign in on this site first.",
		AskAccess:  "The owner has not listed you. Ask them for access.",
	}[d]
	if fix == "" {
		return nil
	}
	if d == TakenDown {
		return web.Refuse(http.StatusGone, string(d), fix) // the live service's answer for a taken-down site
	}
	return web.Refuse(http.StatusForbidden, string(d), fix)
}

// Create makes an empty site for an owner who has a handle. The name must fit
// next to the handle in one 63-character label.
func (s *Sites) Create(ctx context.Context, ownerID, name string) (*Site, error) {
	if err := access.ValidName(name); err != nil {
		return nil, err
	}
	o, err := s.People.Get(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	if o.Handle == "" {
		return nil, web.Refuse(http.StatusConflict, "no_handle", "Pick a handle first. Sites live under it.")
	}
	if len(name)+2+len(o.Handle) > 63 {
		return nil, web.Refuse(http.StatusBadRequest, "name_too_long", fmt.Sprintf("Use a site name of at most %d characters.", 61-len(o.Handle)))
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO sites (owner_id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, ownerID, name)
	if err != nil {
		return nil, err
	}
	s.Forget()
	return s.Get(ctx, ownerID, name)
}

// VersionDir is where version n of a site lives.
func (s *Sites) VersionDir(siteID string, n int) string {
	return filepath.Join(s.Dir, "sites", siteID, fmt.Sprintf("v%d", n))
}

// Stage makes an empty folder to write a new version into. Commit it, or
// remove it.
func (s *Sites) Stage(site *Site) (string, error) {
	b := make([]byte, 8)
	rand.Read(b)
	dir := filepath.Join(s.Dir, "sites", site.ID, "staging-"+hex.EncodeToString(b))
	return dir, os.MkdirAll(dir, 0o755)
}

// Commit makes a staged folder the site's next version and puts it live on
// every address at once. Versions beyond the kept count are removed.
func (s *Sites) Commit(ctx context.Context, site *Site, dir, authorID string) (int, error) {
	var bytes int64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
			}
		}
		return nil
	})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT max(n) FROM versions WHERE site_id = s.id), 0) + 1 FROM sites s WHERE s.id = $1 FOR UPDATE`, site.ID).Scan(&n); err != nil {
		return 0, err
	}
	var author any
	if authorID != "" {
		author = authorID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO versions (site_id, n, author_id, bytes) VALUES ($1, $2, $3, $4)`, site.ID, n, author, bytes); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET live_version = $2, changed_at = now() WHERE id = $1`, site.ID, n); err != nil {
		return 0, err
	}
	final := s.VersionDir(site.ID, n)
	if err := os.Rename(dir, final); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		os.RemoveAll(final)
		return 0, err
	}
	s.Forget()
	rows, err := s.DB.QueryContext(ctx, `DELETE FROM versions WHERE site_id = $1 AND n <= $2 RETURNING n`, site.ID, n-int(limits.KeptVersions.N))
	if err != nil {
		return n, err
	}
	defer rows.Close()
	for rows.Next() {
		var old int
		if rows.Scan(&old) == nil {
			os.RemoveAll(s.VersionDir(site.ID, old))
		}
	}
	return n, rows.Err()
}

// Version is one kept version of a site.
type Version struct {
	N      int       `json:"n"`
	Author string    `json:"author"` // the author's handle or email, "" when gone
	At     time.Time `json:"at"`
	Bytes  int64     `json:"bytes"`
}

// Versions is the site's kept versions, newest first.
func (s *Sites) Versions(ctx context.Context, siteID string) ([]Version, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.n, COALESCE(n.name, p.email, ''), v.created_at, v.bytes FROM versions v
		LEFT JOIN people p ON p.id = v.author_id LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL
		WHERE v.site_id = $1 ORDER BY v.n DESC`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.N, &v.Author, &v.At, &v.Bytes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Rehome moves an owner whose handle changed to the flat address shape
// (<site>--<new handle>.<SiteDomain>): an existing account's .app addresses
// rest on a certificate for the old handle.
func (s *Sites) Rehome(ctx context.Context, q db.Q, ownerID string) error {
	_, err := q.ExecContext(ctx, `UPDATE owner_addresses SET app_shape = false WHERE owner_id = $1`, ownerID)
	return err
}

// URL is the site's own address, the one a tool returns.
func (s *Sites) URL(site *Site) string {
	if site.AppShape {
		return "https://" + site.Name + "." + site.OwnerHandle + "." + s.AppDomain + "/"
	}
	return "https://" + site.Name + "--" + site.OwnerHandle + "." + s.SiteDomain + "/"
}

var errNoKey = errors.New("passcodes are not available: PASSCODE_ENC_KEY is not set")

// Partner is the www or bare partner of an own domain: www.<apex> for an
// apex, the apex for www.<apex>, "" for any other name.
func Partner(host string) string {
	apex := func(h string) bool { d, err := publicsuffix.EffectiveTLDPlusOne(h); return err == nil && d == h }
	if rest, ok := strings.CutPrefix(host, "www."); ok && apex(rest) {
		return rest
	}
	if apex(host) {
		return "www." + host
	}
	return ""
}
