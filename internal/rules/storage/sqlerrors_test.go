package storage

import "testing"

func TestSQLiteTextDropsPathsAndPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"unable to open /srv/simple-host/sites/u/s/runtime/sqlite/db.sqlite": "unable to open [path]",
		"cannot use db.sqlite-wal here":                                      "cannot use [path] here",
		`near "/": syntax error`:                                             `near "/": syntax error`,
	} {
		if got := SQLPathPattern.ReplaceAllString(in, "[path]"); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if got := SQLSecondStatement(" ; /* x */ -- y\n  SELECT 2"); got != "SELECT 2" {
		t.Errorf("second statement %q", got)
	}
	if got := SQLSecondStatement(" ;\n-- trailing"); got != "" {
		t.Errorf("comment counted as statement: %q", got)
	}
}
