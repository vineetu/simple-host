package storage

import (
	"context"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// Inspection exposes a catalog only for the duration of Inspect's callback.
type Inspection struct{ conn *sqlite3.Conn }

// Inspect runs a bounded inspection inside the site's shared admission slot.
func (s *Service) Inspect(ctx context.Context, c Call, write bool, fn func(*Inspection) error) error {
	return s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := s.openTableConn(ctx, c, write)
		if err != nil {
			return err
		}
		defer closeSQLite(func() { defer done(); s.sqliteStage("close") })
		s.sqliteStage("after_open")
		return fn(&Inspection{conn: conn})
	})
}

// Tables lists the resource's application tables.
func (i *Inspection) Tables() (map[string]bool, error) { return runtime.SQLTables(i.conn) }

// TableInfo describes the selected table's columns in storage order.
func (i *Inspection) TableInfo(table string) (map[string]string, []string, error) {
	return runtime.TableInfo(i.conn, table)
}
