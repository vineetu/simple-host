// Package sqlconsole is the SQL console of a site's SQLite databases: POST
// .../storage/sqlite/{name}/query, execute and schema, the SQLite authorizer
// that keeps each mode to its kind of statement, and the plain-words refusals
// for SQLite's errors. Visitors reach it only where the rules allow; the
// owner's tools always.
package sqlconsole
