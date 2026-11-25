// Package access (access/core) owns people, names, and every credential:
// it turns a key, an AI app's token, a dashboard session or a visitor's site
// session into the same answer, a person.
package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"simplehost/core/db"
	"simplehost/core/web"
)

// Schema is the access module's tables. Applied first.
//
//go:embed schema.sql
var Schema string

// People reads and writes the access tables.
type People struct{ DB *sql.DB }

// Person is a person or a team. Handle is "" when they have none yet.
type Person struct {
	ID          string
	Email       string
	DisplayName string
	Handle      string
	IsTeam      bool
	Operator    bool
	Suspended   bool
}

// Hash is how every key, token and secret is stored and looked up: hex
// SHA-256 (the old service's HashAPIKey and HashSecret, so carried hashes
// still match). Fast is right: the secrets are 256 random bits.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// NewSecret is 32 random bytes, URL-safe, with a short readable prefix.
func NewSecret(prefix string) string {
	b := make([]byte, 32)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

var (
	errBadCredential = web.Refuse(http.StatusUnauthorized, "bad_credential", "This key or token does not work. Make a new key in the dashboard, or connect the app again.")
	errForged        = web.Refuse(http.StatusForbidden, "csrf_header", "Send the header X-SH-CSRF: 1 with every change made from a page, and send it from a page on this same address.")
	errSuspended     = web.Refuse(http.StatusForbidden, "account_suspended", "This account is suspended. Write to support if you think this is a mistake.")
)

const (
	sessionLife     = 30 * 24 * time.Hour
	siteSessionLife = 30 * 24 * time.Hour
)

// Caller resolves the request's credential to a person. On a site address
// (siteID set) it reads a key (X-API-Key) or app token (Bearer), or that site's visitor session;
// on the platform it reads a key or app token, or the dashboard session. A
// bad key is refused; a stale cookie is just nobody. A cookie counts only
// under web.FromOwnPage, and never on a web.KeyOnly path. A suspended person
// is refused here, so no feature has to check.
func (p *People) Caller(r *http.Request, siteID string) (web.Caller, error) {
	ctx := r.Context()
	var id, via string
	var err error
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" { // the header scripts use today
		via = "key"
		err = p.DB.QueryRowContext(ctx, `UPDATE keys SET last_used_at = now() WHERE hash = $1 RETURNING person_id`, Hash(key)).Scan(&id)
		if err == sql.ErrNoRows {
			return web.Caller{}, errBadCredential
		}
	} else if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		via = "app"
		err = p.DB.QueryRowContext(ctx, `SELECT person_id FROM oauth_tokens WHERE hash = $1 AND kind = 'access' AND expires_at > now()`, Hash(strings.TrimSpace(tok))).Scan(&id)
		if err == sql.ErrNoRows {
			return web.Caller{}, errBadCredential
		}
	} else if siteID != "" {
		via = "site"
		err = p.DB.QueryRowContext(ctx, `SELECT person_id FROM visitor_sessions WHERE id = $1 AND site_id = $2 AND expires_at > now()`, SiteSessionID(Cookie(r, "sh_vsess")), siteID).Scan(&id)
	} else if web.KeyOnly(r) {
		return web.Caller{}, nil
	} else {
		via = "session"
		err = p.DB.QueryRowContext(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id_hash = $1 AND expires_at > now() RETURNING person_id`, Hash(Cookie(r, "sh_session"))).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return web.Caller{}, nil
	}
	if err != nil {
		return web.Caller{}, err
	}
	if (via == "site" || via == "session") && !web.FromOwnPage(r) {
		return web.Caller{}, errForged
	}
	who, err := p.Get(ctx, id)
	if err != nil {
		return web.Caller{}, err
	}
	if who.Suspended {
		return web.Caller{}, errSuspended
	}
	c := web.Caller{ID: who.ID, Email: who.Email, Operator: who.Operator, Via: via}
	if via == "site" {
		c.SiteID = siteID
	}
	return c, nil
}

// Cookie reads the __Host- form over HTTPS (a sibling host cannot plant it)
// and the plain form over HTTP.
func Cookie(r *http.Request, name string) string {
	if web.HTTPS(r) {
		name = "__Host-" + name
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// SetCookie sets a host-only cookie in the same two forms. A zero life
// removes it.
func SetCookie(w http.ResponseWriter, r *http.Request, name, value string, life time.Duration) {
	maxAge := int(life.Seconds())
	if maxAge == 0 {
		maxAge = -1
	}
	if web.HTTPS(r) {
		name = "__Host-" + name
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: web.HTTPS(r), SameSite: http.SameSiteLaxMode})
}

// StartSession signs personID in to the dashboard in this browser.
func (p *People) StartSession(w http.ResponseWriter, r *http.Request, personID string) error {
	secret := NewSecret("")
	_, err := p.DB.ExecContext(r.Context(), `INSERT INTO sessions (id_hash, person_id, expires_at) VALUES ($1, $2, $3)`, Hash(secret), personID, time.Now().Add(sessionLife))
	if err == nil {
		SetCookie(w, r, "sh_session", secret, sessionLife)
	}
	return err
}

// StartSiteSession signs personID in on this one site, on this host.
func (p *People) StartSiteSession(w http.ResponseWriter, r *http.Request, personID, siteID string) error {
	raw := make([]byte, 32)
	rand.Read(raw)
	cookie := hex.EncodeToString(raw)
	_, err := p.DB.ExecContext(r.Context(), `INSERT INTO visitor_sessions (id, person_id, site_id, host, expires_at) VALUES ($1, $2, $3, $4, $5)`, SiteSessionID(cookie), personID, siteID, web.Host(r), time.Now().Add(siteSessionLife))
	if err == nil {
		SetCookie(w, r, "sh_vsess", cookie, siteSessionLife)
	}
	return err
}

// SiteSessionID is how a visitor session is stored and looked up: the
// SHA-256 of the cookie's 32 bytes (the cookie is their hex), so the
// database never holds a working cookie. The copy tool stores old sessions
// the same way: sha256(id).
func SiteSessionID(cookie string) []byte {
	raw, _ := hex.DecodeString(cookie)
	sum := sha256.Sum256(raw)
	return sum[:]
}

// NewKey makes a named key for personID and returns it. It is shown once.
func (p *People) NewKey(ctx context.Context, personID, name string) (id, key string, err error) {
	key = NewSecret("sh_")
	err = p.DB.QueryRowContext(ctx, `INSERT INTO keys (hash, person_id, name, last4) VALUES ($1, $2, $3, $4) RETURNING id`, Hash(key), personID, name, key[len(key)-4:]).Scan(&id)
	return id, key, err
}

const personColumns = `p.id, COALESCE(p.email, ''), p.display_name, COALESCE(n.name, ''), p.is_team, p.operator, p.suspended_at IS NOT NULL
	FROM people p LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL`

func scanPerson(row *sql.Row) (Person, error) {
	var x Person
	err := row.Scan(&x.ID, &x.Email, &x.DisplayName, &x.Handle, &x.IsTeam, &x.Operator, &x.Suspended)
	if err == sql.ErrNoRows {
		return x, web.NotFound
	}
	return x, err
}

// Get reads a person or team by id.
func (p *People) Get(ctx context.Context, id string) (Person, error) {
	return scanPerson(p.DB.QueryRowContext(ctx, `SELECT `+personColumns+` WHERE p.id = $1`, id))
}

// ByHandle reads the person or team holding a live handle.
func (p *People) ByHandle(ctx context.Context, handle string) (Person, error) {
	return scanPerson(p.DB.QueryRowContext(ctx, `SELECT `+personColumns+` WHERE n.name = $1`, strings.ToLower(handle)))
}

// Ensure returns the person with this email, making them if new. The email
// is the person: one row per email.
func (p *People) Ensure(ctx context.Context, email string) (Person, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return Person{}, web.Refuse(http.StatusBadRequest, "bad_email", "Give a full email address, like name@example.com.")
	}
	var id string
	err := p.DB.QueryRowContext(ctx, `INSERT INTO people (email) VALUES ($1) ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`, email).Scan(&id)
	if err != nil {
		return Person{}, err
	}
	return p.Get(ctx, id)
}

// ActsFor reports whether the caller may act for ownerID: it is them, they
// are a member of that team, or they are the operator.
func (p *People) ActsFor(ctx context.Context, c web.Caller, ownerID string) (bool, error) {
	if c.ID == "" {
		return false, nil
	}
	if c.ID == ownerID || c.Operator {
		return true, nil
	}
	var ok bool
	err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND person_id = $2)`, ownerID, c.ID).Scan(&ok)
	return ok, err
}

// ValidName checks a handle, team name, site name or short name: lower-case
// letters, digits and single hyphens, 1 to 63 characters, never "--".
func ValidName(name string) error {
	ok := len(name) >= 1 && len(name) <= 63 && name[0] != '-' && name[len(name)-1] != '-' && !strings.Contains(name, "--")
	for _, c := range name {
		ok = ok && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
	}
	if !ok {
		return web.Refuse(http.StatusBadRequest, "bad_name", "Use lower-case letters, digits and single hyphens (not --), at most 63 characters.")
	}
	return nil
}

// reserved are names no one may claim as a handle or short name: addresses
// the service itself uses, and names that would pass for the service.
var reserved = map[string]bool{}

// Reserved reports whether name is kept for the service itself.
func Reserved(name string) bool { return reserved[name] }

func init() {
	for _, n := range strings.Fields(`www sites api mcp admin app cname mail email smtp mx ns
		auth login signin signup oauth sso connect dashboard console static assets cdn status help support v1
		root security abuse postmaster hostmaster webmaster noreply no-reply billing payments account
		verify secure official internal localhost simplehost simple-host simplehack simple-hack`) {
		reserved[n] = true
	}
}

// ClaimName gives name (kind "handle" or "short") to a person or a site. A
// reserved, taken or retired name is refused, naming the clash.
func ClaimName(ctx context.Context, q db.Q, name, kind, personID, siteID string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if reserved[name] {
		return web.Refuse(http.StatusConflict, "name_reserved", "The name "+name+" is kept for Simple Host itself. Pick another.")
	}
	var person, site any
	if personID != "" {
		person = personID
	}
	if siteID != "" {
		site = siteID
	}
	res, err := q.ExecContext(ctx, `INSERT INTO names (name, kind, person_id, site_id) VALUES ($1, $2, $3, $4) ON CONFLICT (name) DO NOTHING`, name, kind, person, site)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.Refuse(http.StatusConflict, "name_taken", "The name "+name+" is taken or was used before. Pick another.")
	}
	return nil
}

// RetireName ends a name for good: it stops answering and nobody can take it.
func RetireName(ctx context.Context, q db.Q, name string) error {
	_, err := q.ExecContext(ctx, `UPDATE names SET retired_at = now() WHERE name = $1 AND retired_at IS NULL`, name)
	return err
}

// FirstOperator makes the person with this email the operator, only while
// nobody is (a self-hosted install's first run). ok is false once one exists.
func (p *People) FirstOperator(ctx context.Context, email string) (person Person, ok bool, err error) {
	if person, err = p.Ensure(ctx, email); err != nil {
		return person, false, err
	}
	res, err := p.DB.ExecContext(ctx, `UPDATE people SET operator = true WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM people WHERE operator)`, person.ID)
	if err != nil {
		return person, false, err
	}
	n, _ := res.RowsAffected()
	person.Operator = n == 1
	return person, n == 1, nil
}

// HasOperator reports whether any person is the operator.
func (p *People) HasOperator(ctx context.Context) (bool, error) {
	var yes bool
	err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE operator)`).Scan(&yes)
	return yes, err
}
