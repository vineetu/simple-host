// Package data owns a site's saved data: key-value, SQLite and files, their
// rules, notifications and web push. It may import access and sites, and
// calls them directly (the visitor's address, a site's state, who acts as its
// owner); neither imports it.
//
// Its rules, one function each: the site-wide gates on a storage call
// (Service.Gate, after Access's visitor gate decided PasscodeOK and
// ViewersOK), the resource rules engine (rules_decide.go), and the refusal
// codes whose HTTP status httpx.RefusalStatus decides at the one edge writer,
// datahttp.WriteStorageError.
//
// The one hook it keeps is the product's storage binding provider
// (bindings.go). Its HTTP routes are in datahttp (routes.go).
package data
