// Package datatest holds Data's tests through the whole composed router
// (servertest.NewPrivateApp and friends): who may see and change what, end
// to end. A test that needs only Data's own routes and the modules belongs
// in host/data/datahttp instead. Fixtures shared with the other suites live
// in host/server/servertest.
package datatest
