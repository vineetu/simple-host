// Package routegate holds one source lint: every Go route to a site's
// page-facing data is owner-only or behind the passcode gate. It reads the
// route packages' source, so it lives in a package of its own: a test that
// reads files reruns on every edit to them, and here that costs a second
// instead of rerunning a whole module's suite.
package routegate
