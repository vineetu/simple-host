// Package accesstest holds Access's tests through the whole composed router
// (servertest.NewPrivateApp and friends): who may see and change what, end
// to end. A test that needs only Access's own routes and the modules belongs
// in host/access/accesshttp instead. Fixtures shared with the other suites live
// in host/server/servertest.
package accesstest
