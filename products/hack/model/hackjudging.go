package model

type RubricCriterion struct {
	ID          string
	Position    int
	Name        string
	Description string
	Weight      int
	MaxPoints   int
}

type NamedTeam struct {
	ID, Name, Slug string
}

type NamedJudge struct {
	ID, Name string
	Role     string
}

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
