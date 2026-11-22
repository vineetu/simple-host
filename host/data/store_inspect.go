package data

import (
	"context"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// Inspection exposes a catalog only for the duration of Inspect's callback.
type Inspection struct{ Conn *sqlite3.Conn }

// Inspect runs a bounded inspection inside the site's shared admission slot.
func (s *Service) Inspect(ctx context.Context, c Call, write bool, fn func(*Inspection) error) error {
	return s.Deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := s.OpenTableConn(ctx, c, write)
		if err != nil {
			return err
		}
		defer CloseSQLite(func() { defer done(); s.SQLiteStage("close") })
		s.SQLiteStage("after_open")
		return fn(&Inspection{Conn: conn})
	})
}

// Tables lists the resource's application tables.
func (i *Inspection) Tables() (map[string]bool, error) { return runtime.SQLTables(i.Conn) }

// TableInfo describes the selected table's columns in storage order.
func (i *Inspection) TableInfo(table string) (map[string]string, []string, error) {
	return runtime.TableInfo(i.Conn, table)
}

// TableXInfo includes generated and hidden columns for rules validation.
func (i *Inspection) TableXInfo(table string) (map[string]string, []string, error) {
	return runtime.TableXInfo(i.Conn, table)
}
