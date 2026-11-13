// Package schema carries db/schema.sql inside the binary, so an installation
// that has no psql (a distroless container on Kubernetes) can still build an
// empty database: `simple-host migrate -init`, or MIGRATE_ON_START=true on the
// server. The file stays the one source of truth; nothing here edits it.
package schema

import (
	_ "embed"

	"github.com/vsriram/simple-host/db/migrations"
)

// SQL is the whole of db/schema.sql: plain SQL with no psql meta-commands, so
// one Exec on a lib/pq connection runs it (the migrations test does exactly
// that).
//
//go:embed schema.sql
var SQL string

// Catalog returns the platform schema and migration files.
func Catalog() migrations.Catalog {
	catalog := migrations.Embedded()
	catalog.Schema = SQL
	return catalog
}
