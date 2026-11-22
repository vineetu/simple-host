// Package hacktest is what a Hack feature's suite needs to drive its real
// routes on a disposable database: New builds the core and a test server, the
// feature registers only its own routes on App.Mux, and the setup (events,
// stages, members, teams, entry rules, co-organisers, scores; setup.go) runs
// through the core's functions, never another feature's routes. It imports no
// feature, so a feature change reruns only that feature's suite. Built only by tests.
package hacktest
