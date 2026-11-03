package hackentries

import (
	"github.com/vsriram/simple-host/internal/model"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultEventRubric is what a brand-new event starts with. createEvent is
// the only caller: an event that already exists is never given these rows
// later, and the organiser replaces them with PUT .../rubric.
func DefaultEventRubric() []model.RubricCriterion {
	return []model.RubricCriterion{
		{Name: "Idea", Description: "How original or useful is it?", Weight: 25, MaxPoints: 5},
		{Name: "Execution", Description: "How well does it work?", Weight: 25, MaxPoints: 5},
		{Name: "Design", Description: "How good is the user experience?", Weight: 25, MaxPoints: 5},
		{Name: "Demo", Description: "How clearly was it presented?", Weight: 25, MaxPoints: 5},
	}
}

// ValidateRubric applies the same line and text rules as checkHackLine and
// checkHackText. Every refusal is one invalid_rubric message.
func ValidateRubric(in []struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Weight      *float64 `json:"weight"`
	MaxPoints   *float64 `json:"max_points"`
}) ([]model.RubricCriterion, string) {
	if len(in) < 1 || len(in) > 10 {
		return nil, "Give between 1 and 10 criteria."
	}
	out := make([]model.RubricCriterion, 0, len(in))
	sum := 0
	for _, c := range in {
		name, ok := rubricName(c.Name)
		if !ok {
			return nil, "Give each criterion a name of up to 80 characters."
		}
		desc, msg := rubricDescription(c.Description)
		if msg != "" {
			return nil, msg
		}
		weight, ok := WholeInRange(c.Weight, 0, 100)
		if !ok {
			return nil, "Each weight must be a whole number from 0 to 100."
		}
		points, ok := WholeInRange(c.MaxPoints, 1, 10)
		if !ok {
			return nil, "Each criterion's points must be a whole number from 1 to 10."
		}
		sum += weight
		out = append(out, model.RubricCriterion{
			Name: name, Description: desc, Weight: weight, MaxPoints: points,
		})
	}
	if sum != 100 {
		return nil, "Weights must add up to 100."
	}
	return out, ""
}

// rubricName is checkHackLine for a required criterion name (1–80 runes,
// one line, collapsed spaces, something visible) without writing a response.
func rubricName(s string) (string, bool) {
	if strings.ContainsAny(strings.TrimSpace(s), "\n\r\t\u0085\u2028\u2029") {
		return "", false
	}
	s = strings.Join(strings.Fields(s), " ")
	if !hasVisible(s) {
		return "", false
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", false
	}
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 80 {
		return "", false
	}
	return s, true
}

// rubricDescription is checkHackText for an optional description (0–300
// runes; newlines and tabs stay, other controls do not).
func rubricDescription(s string) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", "A description contains characters that are not allowed."
	}
	if utf8.RuneCountInString(s) > 300 {
		return "", "Keep each description to 300 characters."
	}
	return s, ""
}

// JudgeCommentText cleans and validates a judge's comment on a team: same
// control-character rule as a rubric description (a comment can be empty,
// unlike a description's cousin fields, and also lands in a CSV export, so
// stripping stray formatting here matters for csvSafe's leading-character
// check to actually see the real first character rather than whatever an
// embedded control sequence put there).
func JudgeCommentText(s string) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", "A comment contains characters that are not allowed."
	}
	if utf8.RuneCountInString(s) > 1000 {
		return "", "Keep a comment to 1000 characters."
	}
	return s, ""
}

// WholeInRange reports n when it is a finite whole number in [min, max].
func WholeInRange(n *float64, min, max int) (int, bool) {
	if n == nil || math.IsNaN(*n) || math.IsInf(*n, 0) || *n != math.Trunc(*n) {
		return 0, false
	}
	if *n < float64(min) || *n > float64(max) {
		return 0, false
	}
	return int(*n), true
}

func hasBadControls(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200B, r == 0xFEFF:
			return true
		}
	}
	return false
}

// hasVisible: s has at least one character that shows (not only spaces,
// marks or blank-looking letters such as the Hangul filler).
func hasVisible(s string) bool {
	for _, r := range s {
		switch r {
		case 0x3164, 0x115F, 0x1160, 0xFFA0, 0x2800, 0x180E:
			continue
		}
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r) {
			continue
		}
		return true
	}
	return false
}
