package hackevents

import (
	"github.com/vsriram/simple-host/products/hack/model"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ValidationError struct{ Code, Message string }

var trackSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$`)

func TrackSlug(s string) bool { return trackSlug.MatchString(s) }

func Text(s, field string, min, max int) (string, *ValidationError) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if HasBadControls(s) {
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

func Line(s, field string, min, max int) (string, *ValidationError) {
	if strings.ContainsAny(strings.TrimSpace(s), "\n\r\t\u0085\u2028\u2029") {
		return "", &ValidationError{"invalid_" + field, field + " must be one line"}
	}
	s = strings.Join(strings.Fields(s), " ")
	if min > 0 && !HasVisible(s) {
		return "", &ValidationError{"invalid_" + field, field + " is required"}
	}
	return Text(s, field, min, max)
}

func Questions(in []model.SignupQuestion) ([]model.SignupQuestion, *ValidationError) {
	if len(in) > 8 {
		return nil, &ValidationError{"too_many_questions", "up to eight sign-up questions are allowed"}
	}
	out := make([]model.SignupQuestion, 0, len(in))
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
		prompt, e := Line(q.Prompt, "question", 1, 160)
		if e != nil {
			return nil, e
		}
		out = append(out, model.SignupQuestion{ID: id, Prompt: prompt, Required: q.Required})
	}
	return out, nil
}

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
		name, e := Line(t.Name, "track_name", 1, 80)
		if e != nil {
			return nil, e
		}
		challenge, e := Text(t.Challenge, "challenge", 0, 3000)
		if e != nil {
			return nil, e
		}
		prize, e := Text(t.Prize, "track_prize", 0, 1000)
		if e != nil {
			return nil, e
		}
		out = append(out, model.EventTrack{Slug: slug, Name: name, Challenge: challenge, Prize: prize})
	}
	return out, nil
}

// HasBadControls reports controls that can disguise text (bidi overrides, zero-width space, BOM); joiners, direction marks, soft hyphens and emoji tags stay allowed.
func HasBadControls(s string) bool {
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

// HasVisible reports whether s has at least one character that shows (not only spaces, marks or blank-looking letters such as the Hangul filler).
func HasVisible(s string) bool {
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

// SignupAnswers checks lengths, required answers and unknown question IDs.
func SignupAnswers(questions []model.SignupQuestion, in map[string]string) ([]model.SignupAnswer, *ValidationError) {
	if len(in) > len(questions) {
		return nil, &ValidationError{"invalid_answers", "answer only the listed questions"}
	}
	answers := make([]model.SignupAnswer, 0, len(questions))
	allowed := map[string]bool{}
	for _, q := range questions {
		allowed[q.ID] = true
		answer, e := Text(in[q.ID], "answer", 0, 500)
		if e != nil {
			return nil, e
		}
		if q.Required && answer == "" {
			return nil, &ValidationError{"answer_required", "answer every required question"}
		}
		answers = append(answers, model.SignupAnswer{ID: q.ID, Prompt: q.Prompt, Answer: answer})
	}
	for id := range in {
		if !allowed[id] {
			return nil, &ValidationError{"invalid_answers", "answer only the listed questions"}
		}
	}
	return answers, nil
}

// UniqueError reports the existing database uniqueness error convention.
func UniqueError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}
