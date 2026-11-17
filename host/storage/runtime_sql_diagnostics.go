package storage

import sqlite3 "github.com/ncruces/go-sqlite3"

// LegacyColumnNames returns up to sixty columns in declaration order.
func LegacyColumnNames(conn *sqlite3.Conn, table string) []string {
	if conn == nil {
		return nil
	}
	stmt, _, err := conn.Prepare("SELECT name FROM pragma_table_info(?) ORDER BY cid")
	if err != nil {
		return nil
	}
	defer stmt.Close()
	if stmt.BindText(1, table) != nil {
		return nil
	}
	var cols []string
	for stmt.Step() && len(cols) < 60 {
		cols = append(cols, stmt.ColumnText(0))
	}
	return cols
}

// LegacyForeignKeys describes up to five foreign key references.
func LegacyForeignKeys(conn *sqlite3.Conn, table string) []string {
	stmt, _, err := conn.Prepare(`SELECT "from", "table", coalesce("to", '') FROM pragma_foreign_key_list(?)`)
	if err != nil {
		return nil
	}
	defer stmt.Close()
	if stmt.BindText(1, table) != nil {
		return nil
	}
	var refs []string
	for stmt.Step() && len(refs) < 5 {
		to := stmt.ColumnText(2)
		if to == "" {
			to = "its primary key"
		}
		refs = append(refs, table+"."+stmt.ColumnText(0)+" must match "+stmt.ColumnText(1)+"."+to)
	}
	return refs
}
