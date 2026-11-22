// Package datatest holds Data's route tests: Data's routes alone
// (host/data and its features) on a disposable database, with a stand-in for
// Access's caller lookup (fixture_test.go). They prove what Data decides
// for a given caller: storage rules, quotas, notifications, push and the
// spam check. Which caller Access resolves is accesstest's.
package datatest
