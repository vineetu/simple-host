package mcp

import (
	"net/url"
	"regexp"
	"strings"
)

// Pattern scrubbing for what reaches AgentCat as free text: the stated
// intent and every error, on every product, and every string of a product
// (Simple Hack) tool's copy. The model types the intent, so it can repeat a
// link, code or email address an earlier call returned.
var (
	analyticsEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	analyticsURLPattern   = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>()\[\]{}]+`)
	analyticsUUIDPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	// A key with a known prefix (Simple Host's shk_ and shint_ among them),
	// or any run of 32 or more letters, digits and underscores.
	analyticsKeyPattern = regexp.MustCompile(`\b(?:shk|shint|sk|pk|rk|ghp|gho|ghs|github_pat|xox[abprs]|glpat)[_-][A-Za-z0-9_\-]{8,}|\b[A-Za-z0-9_]{32,}\b`)
	// Join, judge, team and co-organiser codes: 8 or more characters of the
	// code alphabet (no i, l, o, 0 or 1) with at least one digit and one letter.
	analyticsCodePattern = regexp.MustCompile(`(?i)\b[a-hjkmnp-z2-9]{8,64}\b`)
	analyticsDigit       = regexp.MustCompile(`[0-9]`)
	analyticsLetter      = regexp.MustCompile(`[A-Za-z]`)
	// Path segments of a capability link: what follows is the code.
	analyticsLinkPaths = []string{"/join/", "/judge/", "/team/", "/organiser/", "/invite", "/upload", "/photos/", "/download", "/oauth", "/preview/", "/pair"}
	analyticsTokenish  = regexp.MustCompile(`^[A-Za-z0-9_\-]{16,}$`)
)

// scrubAnalyticsText replaces email addresses, capability links (join,
// judge, team and invite links, and any link with a query or a token-like
// path segment), raw IDs, key-like strings and code-shaped words.
func scrubAnalyticsText(text string) string {
	if text == "" {
		return text
	}
	text = analyticsURLPattern.ReplaceAllStringFunc(text, func(link string) string {
		if analyticsCapabilityLink(link) {
			return "[link removed]"
		}
		return link
	})
	text = analyticsEmailPattern.ReplaceAllString(text, "[email removed]")
	text = analyticsUUIDPattern.ReplaceAllString(text, "[id removed]")
	text = analyticsKeyPattern.ReplaceAllStringFunc(text, func(word string) string {
		if analyticsDigit.MatchString(word) && analyticsLetter.MatchString(word) {
			return "[removed]"
		}
		return word
	})
	return analyticsCodePattern.ReplaceAllStringFunc(text, func(word string) string {
		if analyticsDigit.MatchString(word) && analyticsLetter.MatchString(word) {
			return "[code removed]"
		}
		return word
	})
}

func analyticsCapabilityLink(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return true
	}
	path := strings.ToLower(u.EscapedPath())
	for _, part := range analyticsLinkPaths {
		if strings.Contains(path, part) {
			return true
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if analyticsTokenish.MatchString(segment) && analyticsDigit.MatchString(segment) && analyticsLetter.MatchString(segment) {
			return true
		}
	}
	return false
}

// scrubAnalyticsKeys runs the scrubber over every object key: an argument
// name the model made up (an email address as a key) reaches the copy too.
func scrubAnalyticsKeys(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = scrubAnalyticsKeys(v[i])
		}
	case map[string]any:
		for key, child := range v {
			child = scrubAnalyticsKeys(child)
			if clean := scrubAnalyticsText(key); clean != key {
				delete(v, key)
				v[clean] = child
				continue
			}
			v[key] = child
		}
	}
	return value
}

// Product person data. A product's (Simple Hack's) results are full of other
// people: participants, teammates, judges, applicants, message senders. The
// rules below go by the class of a key, not by today's field names, so a new
// field of a known class is removed without a change here.

// analyticsKeptNames are the *_name fields that do not name a person: teams,
// events, tracks, files, sites and the like are sent.
var analyticsKeptNames = map[string]bool{
	"name": true, "teamname": true, "eventname": true, "trackname": true, "filename": true,
	"resourcename": true, "sitename": true, "hostname": true, "cname": true, "toolname": true,
	"prizename": true, "sponsorname": true, "criterionname": true, "confirmname": true,
	"servername": true, "clientname": true, "tablename": true, "columnname": true,
	"teamnames": true, "tracknames": true,
}

// analyticsPersonWords mark an ID field as a person's (user_id, judge_id,
// sender_id, successor_user_id, ...).
var analyticsPersonWords = []string{"user", "account", "judge", "member", "owner", "organiser", "organizer",
	"sender", "author", "creator", "successor", "person", "applicant", "participant", "voter", "admin", "invitee", "viewer"}

// analyticsPersonCollections end the name of a field listing people.
var analyticsPersonCollections = []string{"members", "judges", "people", "participants", "organisers", "organizers",
	"successors", "admins", "others", "applicants", "users", "voters", "staying", "teammates", "invitees"}

// analyticsPersonObjects name a field holding one person.
var analyticsPersonObjects = map[string]bool{
	"user": true, "creator": true, "sender": true, "author": true, "me": true, "judge": true,
	"successor": true, "applicant": true, "participant": true, "person": true, "organiser": true,
	"member": true, "teammate": true, "invitee": true, "voter": true,
}

// analyticsPersonSignals are keys only a person's object carries.
var analyticsPersonSignals = []string{"user_id", "account_id", "judge_id", "judge_user_id", "member_id", "email", "display_name", "username"}

func analyticsEndsWithAny(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func analyticsPersonID(name, key string) bool {
	if name == "meid" || name == "createdby" {
		return true
	}
	if strings.HasSuffix(strings.ToLower(key), "_by") {
		return name != "orderby" && name != "sortby" && name != "groupby"
	}
	if !strings.HasSuffix(name, "id") && !strings.HasSuffix(name, "ids") {
		return false
	}
	for _, word := range analyticsPersonWords {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func analyticsStringValue(field any) bool {
	switch v := field.(type) {
	case string:
		return true
	case []any:
		for _, item := range v {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return len(v) > 0
	}
	return false
}

// analyticsPersonObject reports whether obj describes a person.
func analyticsPersonObject(obj map[string]any) bool {
	for _, key := range analyticsPersonSignals {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

// analyticsProductPersonField reports a product field to remove whole: a
// person's name, ID, contact, a code, or an applicant's free text.
func analyticsProductPersonField(key, name string, field any, obj map[string]any) bool {
	switch {
	case name == "answers" || name == "city" || name == "country" || name == "agerange" || name == "gender":
		return true
	case (name == "name" || name == "id") && analyticsPersonObject(obj):
		return true
	case strings.HasSuffix(name, "name") || strings.HasSuffix(name, "names"):
		return !analyticsKeptNames[name]
	case analyticsPersonID(name, key):
		return true
	case (strings.Contains(name, "email") || strings.Contains(name, "phone")) && analyticsStringValue(field):
		return true
	case strings.HasSuffix(name, "code") && analyticsStringValue(field):
		return true
	case analyticsEndsWithAny(name, analyticsPersonCollections) && analyticsStringValue(field):
		// A list of names or of account IDs (organiser_judges, others).
		return true
	}
	return false
}

// analyticsMarkPeople removes name and id from the people a collection or a
// person field holds; the walk then handles their other fields.
func analyticsMarkPeople(name string, field any) {
	collection := analyticsEndsWithAny(name, analyticsPersonCollections)
	if !collection && !analyticsPersonObjects[name] {
		return
	}
	mark := func(raw any) {
		if person, ok := raw.(map[string]any); ok {
			for _, key := range []string{"name", "id"} {
				if _, has := person[key]; has {
					person[key] = "[removed]"
				}
			}
		}
	}
	if list, ok := field.([]any); ok && collection {
		for _, raw := range list {
			mark(raw)
		}
		return
	}
	mark(field)
}
