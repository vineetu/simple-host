// Package datahttp holds Data's HTTP routes: a site's saved data (KV,
// SQLite, files), notifications, web push and the spam check.
//
// Storage routes are mounted in routes.go; copy a line there, e.g.
//
//	mux.Handle("GET /v1/sites/{sitename}/storage/kv/{name}/keys", route(d.StorageKV))
//
// route runs Access's caller resolution first (the caller middleware the
// composition passes in), so the method receives the resolved
// appstorage.Call. The storage rules and reads/writes live in host/data
// (appstorage.Service): a handler decodes, calls one function and writes its
// result or its refusal through WriteStorageError. Streaming file bodies and
// web push stay here. This package imports the modules, host/web and
// host/httpx, never another route package or a repo package.
package datahttp
