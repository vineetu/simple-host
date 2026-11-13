package storage

import (
	"context"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// Inspection holds a resource connection opened under its storage budget and deadline.
// The caller must run the release function returned by OpenInspection.
type Inspection struct{ conn *sqlite3.Conn }

// OpenInspection opens a bounded storage inspection session. SQLite connections
// stay inside the service; transports consume the session's catalog interface.
func (s *Service) OpenInspection(ctx context.Context, c Call, write bool) (*Inspection, func(), error) {
	conn, done, err := s.openTableConn(ctx, c, write, true)
	if err != nil {
		return nil, nil, err
	}
	return &Inspection{conn: conn}, done, nil
}

// Tables lists the resource's application tables.
func (i *Inspection) Tables() (map[string]bool, error) { return runtime.SQLTables(i.conn) }

// TableInfo describes the selected table's columns in storage order.
func (i *Inspection) TableInfo(table string) (map[string]string, []string, error) {
	return runtime.TableInfo(i.conn, table)
}
