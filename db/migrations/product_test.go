package migrations

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/vsriram/simple-host/internal/testdb"
)

func TestInitProductRecordAtomic(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	set := Set{Platform: Catalog{FS: fstest.MapFS{}, Schema: `CREATE TABLE users(id int);
        CREATE TABLE install_product("only" boolean PRIMARY KEY DEFAULT true CHECK ("only"), product text NOT NULL CHECK (product='simple-host'));`}}
	ctx := context.Background()
	init := func(product string) error {
		return WithLock(ctx, db, func(conn *sql.Conn) error {
			_, err := set.InitConn(ctx, conn, product, nil)
			return err
		})
	}
	if err := init("invalid"); err == nil {
		t.Fatal("invalid record accepted")
	}
	var users, record bool
	if err := db.QueryRow(`SELECT to_regclass('users') IS NOT NULL, to_regclass('install_product') IS NOT NULL`).Scan(&users, &record); err != nil || users || record {
		t.Fatalf("partial schema survived: %v %v %v", users, record, err)
	}
	if err := init("simple-host"); err != nil {
		t.Fatal(err)
	}
	var product string
	if err := db.QueryRow(`SELECT product FROM install_product`).Scan(&product); err != nil || product != "simple-host" {
		t.Fatalf("record=%q: %v", product, err)
	}
}
