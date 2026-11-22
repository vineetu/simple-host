package secrets

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

// MaxPerSite is how many secrets one site may hold.
const MaxPerSite = 20

// ErrTooMany is a save of a new name on a site that already holds MaxPerSite.
var ErrTooMany = errors.New("secrets: the site holds the most secrets it may")

// Secret is what anyone is ever shown of a saved secret: never its value.
// Attach is how the value goes with a request to Destination: as the header
// or query parameter AttachName, after AttachPrefix.
type Secret struct {
	Name         string     `json:"name"`
	Destination  string     `json:"destination"`
	Who          string     `json:"who"`
	Attach       string     `json:"attach"`
	AttachName   string     `json:"attach_name"`
	AttachPrefix string     `json:"attach_prefix"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	UseCount     int64      `json:"use_count"`
}

// Store is the site_secrets table and the master key file that seals it.
type Store struct {
	db      *sql.DB
	keyFile string
}

// New is the store on db, sealing under the master key file at keyFile.
func New(db *sql.DB, keyFile string) *Store { return &Store{db: db, keyFile: keyFile} }

// Save seals value and stores it with s's settings as the site's secret
// s.Name, replacing one of that name. A new name on a site already holding
// MaxPerSite is ErrTooMany: the count and the write are one statement.
func (s *Store) Save(ctx context.Context, siteID, value string, v Secret) error {
	var saved bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM site_secrets)`).Scan(&saved); err != nil {
		return err
	}
	ciphertext, nonce, err := seal(s.keyFile, !saved, siteID, v, value)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO site_secrets (site_id, name, ciphertext, nonce, key_version, destination, who, attach, attach_name, attach_prefix)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		WHERE (SELECT count(*) FROM site_secrets WHERE site_id = $1) < $11
		   OR EXISTS (SELECT 1 FROM site_secrets WHERE site_id = $1 AND name = $2)
		ON CONFLICT (site_id, name) DO UPDATE SET ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce,
		  key_version = EXCLUDED.key_version, destination = EXCLUDED.destination, who = EXCLUDED.who,
		  attach = EXCLUDED.attach, attach_name = EXCLUDED.attach_name, attach_prefix = EXCLUDED.attach_prefix, updated_at = now()`,
		siteID, v.Name, ciphertext, nonce, keyVersion, v.Destination, v.Who, v.Attach, v.AttachName, v.AttachPrefix, MaxPerSite)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTooMany
	}
	return nil
}

const columns = `name, destination, who, attach, attach_name, attach_prefix, created_at, updated_at, last_used_at, use_count`

func scan(row interface{ Scan(...any) error }) (Secret, error) {
	var v Secret
	err := row.Scan(&v.Name, &v.Destination, &v.Who, &v.Attach, &v.AttachName, &v.AttachPrefix, &v.CreatedAt, &v.UpdatedAt, &v.LastUsedAt, &v.UseCount)
	return v, err
}

// List is the site's secrets by name, without their values.
func (s *Store) List(ctx context.Context, siteID string) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM site_secrets WHERE site_id = $1 ORDER BY name`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Delete removes the site's secret name; false when there was none.
func (s *Store) Delete(ctx context.Context, siteID, name string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM site_secrets WHERE site_id = $1 AND name = $2`, siteID, name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Open is the site's secret name in the clear with its settings, read in one
// statement, for the one place that sends it to its destination
// (host/secrets/call), which decides from these same settings whether the
// caller may use it. sql.ErrNoRows when the site has no secret of that name.
func (s *Store) Open(ctx context.Context, siteID, name string) (value string, v Secret, err error) {
	var ciphertext, nonce []byte
	err = s.db.QueryRowContext(ctx, `SELECT ciphertext, nonce, `+columns+` FROM site_secrets WHERE site_id = $1 AND name = $2`, siteID, name).
		Scan(&ciphertext, &nonce, &v.Name, &v.Destination, &v.Who, &v.Attach, &v.AttachName, &v.AttachPrefix, &v.CreatedAt, &v.UpdatedAt, &v.LastUsedAt, &v.UseCount)
	if err != nil {
		return "", Secret{}, err
	}
	value, err = unseal(s.keyFile, siteID, v, ciphertext, nonce)
	if err != nil {
		return "", Secret{}, err
	}
	return value, v, nil
}

// Used counts one use of the site's secret name: a call that was let through.
func (s *Store) Used(ctx context.Context, siteID, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE site_secrets SET use_count = use_count + 1, last_used_at = now() WHERE site_id = $1 AND name = $2`, siteID, name)
	return err
}

// KeyFile is the master key file's path and whether it is there, for the
// line the program logs at start.
func (s *Store) KeyFile() (path string, present bool) {
	_, err := os.Stat(s.keyFile)
	return s.keyFile, err == nil
}
