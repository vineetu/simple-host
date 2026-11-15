package model

// RubricCriterion is a judging record.
type RubricCriterion struct {
	ID          string
	Position    int
	Name        string
	Description string
	Weight      int
	MaxPoints   int
}

// NamedTeam is a judging record.
type NamedTeam struct {
	ID, Name, Slug string
}

// NamedJudge is a judging record.
type NamedJudge struct {
	ID, Name string
	Role     string
}

// JudgeAssignment is a judging record.
type JudgeAssignment struct {
	JudgeID   string `json:"judge_id"`
	JudgeName string `json:"judge_name"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
}

// RawScoreRow is a score with its associated names.
type RawScoreRow struct {
	JudgeID, JudgeName         string
	TeamID, TeamName           string
	CriterionID, CriterionName string
	CriterionPosition          int
	Points, MaxPoints          int
	Comment                    string
}
