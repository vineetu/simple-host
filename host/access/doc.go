// Package access owns who may do what: accounts, keys, sign-in, the
// connector, site passcodes, named viewers, site editors and teams.
// Its HTTP routes are in accesshttp (routes.go); which routes an editor or a
// deploy-only key may call is in auth/scope.go. It imports no other module:
// sites and data import it.
package access
