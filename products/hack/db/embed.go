package db

import (
	_ "embed"
	platform "github.com/vsriram/simple-host/db/migrations"
	"github.com/vsriram/simple-host/products/hack/db/migrations"
)

//go:embed schema.sql
var SQL string

// Catalog returns Hack's schema and migration files.
func Catalog() platform.Catalog {
	catalog := migrations.Embedded()
	catalog.Schema = SQL
	return catalog
}
