// Package data is the core of a site's saved data, the door every storage
// feature and every other module goes through: the caller facts a route
// arrives with (Call, set by Access's StorageCaller), the site-wide gates
// (Service.Gate), resource resolution (ResourceCall, LoadResource), the rules
// decision for KV, SQLite and files (rules_decide.go), SQLite admission and
// the site write lock (sqlite_gate.go, service.go), quota measurement
// (usage.go), the committed-write event (events.go), the product's storage
// bindings (bindings.go, the one hook it keeps) and the route core: Handler,
// the spam check in front of every visitor write, and WriteStorageError.
//
// Each feature is its own package beside it and imports only this core:
// kv, rows, sqlconsole, files, resources, usage, visitors, notifications and
// push. The core never imports a feature. It may import access and sites.
package data
