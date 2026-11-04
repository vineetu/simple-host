package legacy

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// GetDataSettings reads one name's declaration and the site's legacy flag.
// sql.ErrNoRows when the site does not exist.
func GetDataSettings(ctx context.Context, q sqlkit.Querier, siteID, name string) (model.DataSettings, error) {
	s := model.DataSettings{Name: name, Notify: "off"}
	var kind sql.NullString
	var declared sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT s.legacy_data, cs.kind, COALESCE(cs.private, false), COALESCE(cs.one_per_person, false),
		       COALESCE(cs.notify, 'off'), cs.declared_at
		  FROM sites s
		  LEFT JOIN collection_settings cs ON cs.site_id = s.id AND cs.collection = $2
		 WHERE s.id = $1`, siteID, name).Scan(&s.MadeBeforeKinds, &kind, &s.Private, &s.OnePerPerson, &s.Notify, &declared)
	if err != nil {
		return s, err
	}
	s.Shared = s.MadeBeforeKinds
	s.Kind = kind.String
	if declared.Valid {
		t := declared.Time
		s.DeclaredAt = &t
	}
	return s, nil
}

// DeclareData records name's kind and options. Declaring content always
// leaves it public (private = false). A Personal name is always stored
// private: nothing that knows the kinds reads the flag for it, and code that
// does not (an older binary after a rollback) then treats it as owner-only
// instead of public. declared_at is kept from the first declaration.
func DeclareData(ctx context.Context, q sqlkit.Querier, siteID, name, kind string, private, onePerPerson bool, notify string) error {
	if kind == "mine" {
		private = true
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO collection_settings (site_id, collection, private, kind, one_per_person, notify, declared_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		ON CONFLICT (site_id, collection) DO UPDATE SET
		  private = EXCLUDED.private, kind = EXCLUDED.kind, one_per_person = EXCLUDED.one_per_person,
		  notify = EXCLUDED.notify, declared_at = COALESCE(collection_settings.declared_at, now()), updated_at = now()`,
		siteID, name, private, kind, onePerPerson, notify)
	return err
}
