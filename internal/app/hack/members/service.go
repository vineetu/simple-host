package members

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/db"
)

// Service owns Hack membership workflows.
type Service struct {
	db             *sql.DB
	archiveEventTx func(context.Context, *sql.Tx, string, string) (db.Event, error)
	deleteEventTx  func(context.Context, *sql.Tx, db.Event) error
}

// New constructs a membership service.
func New(database *sql.DB, archive func(context.Context, *sql.Tx, string, string) (db.Event, error), deleteEvent func(context.Context, *sql.Tx, db.Event) error) *Service {
	return &Service{db: database, archiveEventTx: archive, deleteEventTx: deleteEvent}
}
