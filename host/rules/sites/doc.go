// Package sites holds pure, deterministic site- and handle-name rules shared
// by Host and Hack HTTP: shape and reservation checks over constant tables
// and regexps, with no SQL, filesystem, provider call or ambient clock. Per
// docs/architecture/target.md §2, it imports only model (not needed today).
package sites
