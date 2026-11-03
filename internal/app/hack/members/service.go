package members

import (
	model "github.com/vsriram/simple-host/internal/model"

	"context"
	"database/sql"
)

// Service owns Hack membership workflows.
type Service struct {
	db                    *sql.DB
	archiveEventTx        func(context.Context, *sql.Tx, string, string) (model.Event, error)
	deleteEventTx         func(context.Context, *sql.Tx, model.Event) error
	afterEnd              func(context.Context, model.Event)
	removeEventFiles      func(model.Event)
	removeOrphanTeamSites func(context.Context, model.Event)
}

// New constructs a membership service.
func New(database *sql.DB, archive func(context.Context, *sql.Tx, string, string) (model.Event, error), deleteEvent func(context.Context, *sql.Tx, model.Event) error, afterEnd func(context.Context, model.Event), removeFiles func(model.Event), removeOrphans func(context.Context, model.Event)) *Service {
	return &Service{db: database, archiveEventTx: archive, deleteEventTx: deleteEvent, afterEnd: afterEnd, removeEventFiles: removeFiles, removeOrphanTeamSites: removeOrphans}
}
