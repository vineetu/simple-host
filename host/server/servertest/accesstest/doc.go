// Package accesstest holds Access's route tests: who may see and change
// what, through Access's routes alone (host/access/accesshttp) on a
// disposable database (fixture_test.go). Sites' page serving and a data
// route are small stand-ins in front of Access's real gates, so a change to
// another module's routes does not rerun this suite.
package accesstest
