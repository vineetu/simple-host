package access

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"simplehost/core/db"
)

// Hop carries a visitor's sign-in from a site to the platform and back,
// sealed so neither leg needs a table. Going out, Person is "". Coming back
// (the ticket), Person is who signed in, having proved the email or
// confirmed this site on the platform.
type Hop struct {
	Site, Host, Return string
	Nonce              string // hash of the nonce cookie on the site host
	Person             string
	Exp                int64
}

// hopKey lives only in this process: a restart drops hops in flight, and
// the visitor simply signs in again.
var hopKey = func() cipher.AEAD {
	k := make([]byte, 32)
	rand.Read(k)
	b, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(b)
	return g
}()

var errBadHop = errors.New("the sign-in link is not valid or has run out; start again")

// SealHop seals h to be opened within life.
func SealHop(h Hop, life time.Duration) string {
	h.Exp = time.Now().Add(life).Unix()
	b, _ := json.Marshal(h)
	nonce := make([]byte, hopKey.NonceSize())
	rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(hopKey.Seal(nonce, nonce, b, nil))
}

// OpenHop opens a sealed hop that has not run out.
func OpenHop(s string) (Hop, error) {
	var h Hop
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) < hopKey.NonceSize() {
		return h, errBadHop
	}
	n := hopKey.NonceSize()
	plain, err := hopKey.Open(nil, b[:n], b[n:], nil)
	if err != nil || json.Unmarshal(plain, &h) != nil || h.Exp < time.Now().Unix() {
		return Hop{}, errBadHop
	}
	return h, nil
}

// SiteConfirmed reports whether the person once confirmed signing in on the
// site, so riding their platform sign-in needs no question.
func (p *People) SiteConfirmed(ctx context.Context, siteID, personID string) (ok bool, err error) {
	err = p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM visitor_signins WHERE site_id = $1 AND person_id = $2 AND confirmed)`, siteID, personID).Scan(&ok)
	return ok, err
}

// EndSignIns signs a person out everywhere: every browser, every site and
// every connected app. Keys stay: they are revoked one by one.
func (p *People) EndSignIns(ctx context.Context, personID string) error {
	for _, q := range []string{`DELETE FROM sessions WHERE person_id = $1`, `DELETE FROM visitor_sessions WHERE person_id = $1`,
		`DELETE FROM oauth_grants WHERE person_id = $1`} {
		if _, err := p.DB.ExecContext(ctx, q, personID); err != nil {
			return err
		}
	}
	return nil
}

// NewTeam makes a team row (a person with no email) and returns its id.
func NewTeam(ctx context.Context, q db.Q, displayName string) (id string, err error) {
	err = q.QueryRowContext(ctx, `INSERT INTO people (is_team, display_name) VALUES (true, $1) RETURNING id`, displayName).Scan(&id)
	return id, err
}

// Remove deletes a person or team for good. Their handle is retired first,
// so nobody can take it.
func Remove(ctx context.Context, q db.Q, id string) error {
	if _, err := q.ExecContext(ctx, `UPDATE names SET retired_at = now() WHERE person_id = $1 AND kind = 'handle' AND retired_at IS NULL`, id); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM people WHERE id = $1`, id)
	return err
}

// Rename sets a person's or team's display name. It is in no address.
func (p *People) Rename(ctx context.Context, id, displayName string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE people SET display_name = $2 WHERE id = $1`, id, strings.TrimSpace(displayName))
	return err
}
