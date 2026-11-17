// Package auth holds pure, deterministic OAuth-grant decisions shared by
// Host and Hack HTTP: scope classification over constant tables and a shape
// check, with no SQL, HTTP, filesystem, provider call or ambient clock. Per
// docs/architecture/target.md §2, it imports only model (not needed today).
package auth
