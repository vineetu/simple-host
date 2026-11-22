// Package kv is a site's key-value namespaces: list, read, add or edit (the
// server decides which, under the write lock) and delete keys, each through
// the resource's rules (data's authorize), with their routes
// (/v1/sites/{sitename}/storage/kv/...). Keys live in one SQLite file per
// namespace (host/storage); creating and clearing that file is the data
// core's, because declaring a resource does it.
package kv
