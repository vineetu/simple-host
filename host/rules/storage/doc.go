// Package storage holds pure, deterministic site-storage arithmetic shared
// by Host and Hack HTTP, over limits its callers have already resolved: no
// SQL, HTTP, filesystem, provider call or ambient clock. Per
// docs/architecture/target.md §2, it imports only model (not needed today).
package storage
