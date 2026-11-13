package rules

import "fmt"

type RuleError struct {
	Table    string `json:"table"`
	Policy   string `json:"policy"`
	Clause   string `json:"clause"`
	Position int    `json:"position"`
	Message  string `json:"message"`
}

func (e RuleError) Error() string {
	return fmt.Sprintf("rules %s policy %q at %d: %s", e.Clause, e.Policy, e.Position, e.Message)
}

func refusal(clause string, pos int, message string) *RuleError {
	return &RuleError{Clause: clause, Position: pos, Message: message}
}
