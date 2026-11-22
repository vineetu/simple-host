// Package rows is the rules-governed row API of a site's SQLite databases:
// visitors send an action and values, never SQL, and the server builds each
// statement from the table's rules (read, add, edit, delete, add for
// someone), with its routes (.../storage/sqlite/{name}/tables/{table}/rows).
// The rule decision, the table's metadata and the connection are the data
// core's, because the console and the declaration use them too.
package rows
