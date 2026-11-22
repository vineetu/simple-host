package entries

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/products/hack/app/entries"

	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// TextInput is a participant's entry text change: nil leaves a field as is.
type TextInput struct {
	UpdatedAt   *string
	Title       *string
	Tagline     *string
	Description *string
	VideoURL    *string
	CodeURL     *string
	Problem     *string
	Audience    *string
	Tools       *[]string
	Maturity    *string
	Track       *string
}

// entryToolsMax is how many tools an entry may list.
const entryToolsMax = 10

// textField is one free-text entry field and its bounds; line fields refuse
// line breaks.
type textField struct {
	name     string
	line     bool
	max      int
	value    func(TextInput) *string
	validURL bool
}

// textFields are the entry's text fields in the order they are checked.
var textFields = []textField{
	{name: "title", line: true, max: 80, value: func(in TextInput) *string { return in.Title }},
	{name: "tagline", line: true, max: 160, value: func(in TextInput) *string { return in.Tagline }},
	{name: "description", max: 5000, value: func(in TextInput) *string { return in.Description }},
	{name: "video_url", validURL: true, value: func(in TextInput) *string { return in.VideoURL }},
	{name: "code_url", validURL: true, value: func(in TextInput) *string { return in.CodeURL }},
	{name: "problem", max: 140, value: func(in TextInput) *string { return in.Problem }},
	{name: "audience", max: 100, value: func(in TextInput) *string { return in.Audience }},
}

var maturities = map[string]bool{"": true, "working": true, "partial": true, "concept": true}

func TextWrite(in TextInput) (entries.WriteRequest, error) {
	req := entries.WriteRequest{Kind: "text", UpdatedAt: in.UpdatedAt, Fields: map[string]string{}}
	for _, f := range textFields {
		if v := f.value(in); v != nil {
			s, err := f.clean(*v)
			if err != nil {
				return req, err
			}
			req.Fields[f.name] = s
		}
	}
	if in.Maturity != nil {
		if !maturities[*in.Maturity] {
			return req, &entries.Error{Code: "invalid_maturity", Message: "maturity must be working, partial, concept or empty"}
		}
		req.Fields["maturity"] = *in.Maturity
	}
	if in.Tools != nil {
		tools, err := cleanTools(*in.Tools)
		if err != nil {
			return req, err
		}
		req.Tools = &tools
	}
	if in.Track != nil {
		track := strings.TrimSpace(*in.Track)
		if track != "" && !hackevents.TrackSlug(track) {
			return req, &entries.Error{Code: "invalid_track", Message: "choose a listed track"}
		}
		req.Track = &track
	}
	return req, nil
}

func (f textField) clean(s string) (string, error) {
	if f.validURL {
		return s, checkURL(s, f.name)
	}
	check := hackevents.Text
	if f.line {
		check = hackevents.Line
	}
	v, e := check(s, f.name, 0, f.max)
	if e != nil {
		return "", &entries.Error{Code: e.Code, Message: e.Message}
	}
	return v, nil
}

// cleanTools checks the tool names and drops case-insensitive repeats.
func cleanTools(in []string) ([]string, error) {
	if len(in) > entryToolsMax {
		return nil, &entries.Error{Code: "invalid_tools", Message: "tools takes at most 10 items"}
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		s, e := hackevents.Line(raw, "tools", 1, 30)
		if e != nil {
			return nil, &entries.Error{Code: e.Code, Message: e.Message}
		}
		if key := strings.ToLower(s); !seen[key] {
			seen[key] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// checkURL accepts "" or a clean absolute http(s) URL with a host, at most 500 characters.
func checkURL(s, field string) error {
	if s == "" || utf8.RuneCountInString(s) <= 500 && !strings.ContainsFunc(s, spaceOrControl) && absoluteHTTPURL(s) {
		return nil
	}
	return &entries.Error{Code: "invalid_url", Message: field + " must be an absolute http or https URL of at most 500 characters"}
}

func spaceOrControl(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

func absoluteHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Host != ""
}
