package hackentries

import (
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
)

// Judgeable reports whether an entry is eligible for judging. The SQL query
// implementation is repository/hackentries.EntryJudgeableSQL; settled incomplete entries qualify.
func Judgeable(e model.EventEntry) bool {
	return e.Exists && (e.SubmittedAt.Valid || e.SubmitSettledAt.Valid) && !e.ExcludedAt.Valid
}

// JudgeableCase describes an entry and its expected eligibility.
type JudgeableCase struct {
	Name  string
	Entry model.EventEntry
	Want  bool
}

// JudgeableCases is the shared set of Go and SQL eligibility cases.
var JudgeableCases = []JudgeableCase{
	{"no entry row", model.EventEntry{}, false},
	{"draft", model.EventEntry{Exists: true}, false},
	{"submitted", model.EventEntry{Exists: true, SubmittedAt: sql.NullTime{Valid: true}}, true},
	{"settled complete", model.EventEntry{Exists: true, SubmittedAt: sql.NullTime{Valid: true}, SubmitSettledAt: sql.NullTime{Valid: true}}, true},
	{"settled incomplete", model.EventEntry{Exists: true, SubmitSettledAt: sql.NullTime{Valid: true}}, true},
	{"excluded submitted", model.EventEntry{Exists: true, SubmittedAt: sql.NullTime{Valid: true}, ExcludedAt: sql.NullTime{Valid: true}}, false},
	{"excluded settled", model.EventEntry{Exists: true, SubmitSettledAt: sql.NullTime{Valid: true}, ExcludedAt: sql.NullTime{Valid: true}}, false},
}
