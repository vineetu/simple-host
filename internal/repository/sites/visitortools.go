package sites

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// VisitorSitesQuerier adds row iteration to the shared query interface.
type VisitorSitesQuerier interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// VisitorSharedSite identifies a site shared with a signed-in email.
type VisitorSharedSite struct{ ID, Name, Owner string }

// VisitorSharedSites lists shared sites in name order.
func VisitorSharedSites(ctx context.Context, q VisitorSitesQuerier, email string) ([]VisitorSharedSite, error) {
	rows, err := q.QueryContext(ctx, `SELECT s.id, s.name, s.user_id FROM sites s JOIN site_viewers v ON v.site_id=s.id WHERE v.email=$1 AND s.deleted_at IS NULL ORDER BY s.name`, email)
	if err != nil {
		return nil, err
	}
	list := []VisitorSharedSite{}
	for rows.Next() {
		var x VisitorSharedSite
		if err := rows.Scan(&x.ID, &x.Name, &x.Owner); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return list, nil
}
