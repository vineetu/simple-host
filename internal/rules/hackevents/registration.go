package hackevents

import (
	"github.com/vsriram/simple-host/internal/model"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidationError describes a rejected field.
type ValidationError struct{ Code, Message string }

// Question is a sign-up question.
type Question struct {
	ID, Prompt string
	Required   bool
}

var trackSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$`)

// TrackSlug reports whether a track or question slug is valid.
func TrackSlug(s string) bool { return trackSlug.MatchString(s) }
func text(s, field string, min, max int) (string, *ValidationError) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", &ValidationError{"invalid_" + field, field + " contains characters that are not allowed"}
	}
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		if min > 0 && n < min {
			return "", &ValidationError{"invalid_" + field, field + " is required"}
		}
		return "", &ValidationError{"invalid_" + field, field + " is too long"}
	}
	return s, nil
}
func line(s, field string, min, max int) (string, *ValidationError) {
	if strings.ContainsAny(strings.TrimSpace(s), "\n\r\t\u0085\u2028\u2029") {
		return "", &ValidationError{"invalid_" + field, field + " must be one line"}
	}
	s = strings.Join(strings.Fields(s), " ")
	if min > 0 && !hasVisible(s) {
		return "", &ValidationError{"invalid_" + field, field + " is required"}
	}
	return text(s, field, min, max)
}

// Text validates and normalizes a text field.
func Text(s, field string, min, max int) (string, *ValidationError) { return text(s, field, min, max) }

// Line validates and normalizes a single-line field.
func Line(s, field string, min, max int) (string, *ValidationError) { return line(s, field, min, max) }

// Questions validates and normalizes sign-up questions.
func Questions(in []Question) ([]Question, *ValidationError) {
	if len(in) > 8 {
		return nil, &ValidationError{"too_many_questions", "up to eight sign-up questions are allowed"}
	}
	out := make([]Question, 0, len(in))
	seen := map[string]bool{}
	for i, q := range in {
		id := strings.TrimSpace(q.ID)
		if id == "" {
			id = "q" + strconv.Itoa(i+1)
		}
		if !TrackSlug(id) || seen[id] {
			return nil, &ValidationError{"invalid_question_id", "question ids must be unique lowercase names"}
		}
		seen[id] = true
		prompt, e := line(q.Prompt, "question", 1, 160)
		if e != nil {
			return nil, e
		}
		out = append(out, Question{id, prompt, q.Required})
	}
	return out, nil
}

// Tracks validates and normalizes event tracks.
func Tracks(in []model.EventTrack) ([]model.EventTrack, *ValidationError) {
	if len(in) > 12 {
		return nil, &ValidationError{"too_many_tracks", "up to twelve tracks are allowed"}
	}
	out := make([]model.EventTrack, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		slug := strings.TrimSpace(t.Slug)
		if !TrackSlug(slug) || seen[slug] {
			return nil, &ValidationError{"invalid_track_slug", "track " + t.Name + ": slug must be a unique lowercase name up to 80 characters"}
		}
		seen[slug] = true
		name, e := line(t.Name, "track_name", 1, 80)
		if e != nil {
			return nil, e
		}
		challenge, e := text(t.Challenge, "challenge", 0, 3000)
		if e != nil {
			return nil, e
		}
		prize, e := text(t.Prize, "track_prize", 0, 1000)
		if e != nil {
			return nil, e
		}
		out = append(out, model.EventTrack{Slug: slug, Name: name, Challenge: challenge, Prize: prize})
	}
	return out, nil
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
