// Package sitestest holds Sites's route tests: Sites's routes alone
// (host/sites/siteshttp with the host routing chain in front, fixture_test.go)
// on a disposable database, with a stand-in for Access's visitor gate. Who
// may deploy and change what, addresses, domains, exports, deletes and the
// operator holds on every host kind Sites serves. Unlocking a protected site
// is accesstest's; the whole composed router is host/server/servertest's.
package sitestest
