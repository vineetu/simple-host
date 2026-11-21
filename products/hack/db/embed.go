package db

import (
	_ "embed"
	platform "github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/products/hack/db/migrations"
)

//go:embed schema.sql
var SQL string

func Catalog() platform.Catalog {
	catalog := migrations.Embedded()
	catalog.Schema = SQL
	return catalog
}
