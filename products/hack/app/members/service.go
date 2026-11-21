package members

import (
	"database/sql"

	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
)

// Service owns Hack membership workflows.
type Service struct {
	db      *sql.DB
	events  *eventapp.Service
	entries *entryapp.Service
}

func New(database *sql.DB, events *eventapp.Service, entries *entryapp.Service) *Service {
	return &Service{db: database, events: events, entries: entries}
}
