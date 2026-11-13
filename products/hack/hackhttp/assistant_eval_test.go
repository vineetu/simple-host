package hackhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/llm"
	"github.com/vsriram/simple-host/internal/webassets"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// assistantEvalCase is one row of testdata/assistant_eval.json (organiser
// assistant v2, 2026-11-09: every organiser settings area, four fixture
// events at different stages).
//
//   - fixture: which event the question is asked about ("open" when
//     empty, or "draft", "judging" (locked), "ended").
//   - screen/tab: sent with the question, as the browser does.
//   - expect_start: "yes" or "no" -- the answer's first word must be it
//     (the prompt asks for Yes/No first on yes-or-no questions); an
//     answer that opens with the other one is a wrong answer.
//   - expect_all: groups of acceptable phrases; every group must match.
//   - expect_text_any: one whole-word fact (event data questions).
//   - expect_href_any: one of these links must be in the answer.
//   - forbid_any: contradictions; any one is a wrong answer.
//   - expect_unsure: a deliberately unanswerable question; the answer
//     must say it is not sure.
type assistantEvalCase struct {
	Area          string     `json:"area"`
	Question      string     `json:"question"`
	Fixture       string     `json:"fixture,omitempty"`
	Screen        string     `json:"screen,omitempty"`
	Tab           string     `json:"tab,omitempty"`
	ExpectStart   string     `json:"expect_start,omitempty"`
	ExpectAll     [][]string `json:"expect_all,omitempty"`
	ExpectHrefAny []string   `json:"expect_href_any,omitempty"`
	ExpectTextAny []string   `json:"expect_text_any,omitempty"`
	ForbidAny     []string   `json:"forbid_any,omitempty"`
	ExpectUnsure  bool       `json:"expect_unsure,omitempty"`
	MaxLines      int        `json:"max_lines,omitempty"`
}

func (c assistantEvalCase) fixture() string {
	if c.Fixture == "" {
		return "open"
	}
	return c.Fixture
}

func (c assistantEvalCase) maxLines() int {
	if c.MaxLines <= 0 {
		return 4
	}
	return c.MaxLines
}

func loadAssistantEvalCases(t *testing.T) []assistantEvalCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/assistant_eval.json")
	if err != nil {
		t.Fatalf("read assistant_eval.json: %v", err)
	}
	var cases []assistantEvalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse assistant_eval.json: %v", err)
	}
	return cases
}

// withSlug fills {slug} into a case's expected links for one fixture.
func (c assistantEvalCase) withSlug(slug string) assistantEvalCase {
	out := c
	out.ExpectHrefAny = make([]string, len(c.ExpectHrefAny))
	for i, h := range c.ExpectHrefAny {
		out.ExpectHrefAny[i] = strings.ReplaceAll(h, "{slug}", slug)
	}
	return out
}

// assistantValidHrefs is every href the model is allowed to use for this
// event: the organiser navigation targets, plus the judge ones only when
// canJudge (same rule the system prompt's PLACES section uses --
// AssistantService.buildSystemPrompt gates judge targets on canJudge,
// which hack_assistant.go computes as events.MembershipAbilities(member).Judge;
// the caller passes that same bool here rather than this test
// re-deriving its own notion of "can judge"), plus the four Hack docs
// pages' /docs/... URLs. A made-up link is a wrong answer.
func assistantValidHrefs(t *testing.T, slug string, canJudge bool) map[string]bool {
	t.Helper()
	var nav struct {
		Targets []struct {
			Role        string `json:"role"`
			HrefPattern string `json:"href_pattern"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(events.Navigation(), &nav); err != nil {
		t.Fatalf("parse navigation.json: %v", err)
	}
	out := map[string]bool{
		"/docs/start/":       true,
		"/docs/start/judge/": true,
		"/docs/start/join/":  true,
		"/docs/limits/":      true,
	}
	for _, target := range nav.Targets {
		if target.Role != "organiser" && !(canJudge && target.Role == "judge") {
			continue
		}
		href := strings.ReplaceAll(target.HrefPattern, "{slug}", slug)
		if href == "" || strings.Contains(href, "{") {
			continue
		}
		out[href] = true
	}
	return out
}

// normaliseAssistantHref mirrors assistant.js normaliseHref: the browser
// drops whitespace and invisible characters inside an href and reads a
// leading "//e/" or "//docs/" as the app path, then shows a link only for
// /e/<slug>/ and /docs/. The grader judges the link the organiser would get.
func normaliseAssistantHref(h string) string {
	h = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || (r >= 0x200b && r <= 0x200f) || r == 0x2060 || r == 0xfeff {
			return -1
		}
		return r
	}, h)
	if strings.HasPrefix(h, "//e/") || strings.HasPrefix(h, "//docs/") {
		h = h[1:]
	}
	return h
}

// cutHrefTitle drops a markdown link title ([x](/path "title") -> /path):
// cuts raw at the first space that follows at least one non-space rune.
// A *leading* space is left alone here -- that is the normaliser's job
// (normaliseAssistantHref strips it, along with invisible characters), so
// a genuinely leading space never gets mistaken for the title separator.
func cutHrefTitle(raw string) string {
	seenNonSpace := false
	for i, r := range raw {
		if unicode.IsSpace(r) {
			if seenNonSpace {
				return raw[:i]
			}
			continue
		}
		seenNonSpace = true
	}
	return raw
}

// answerHrefs extracts every markdown [label](href) href from an answer,
// with any link title cut off before the href is normalised.
func answerHrefs(answer string) []string {
	var out []string
	rest := answer
	for {
		open := strings.Index(rest, "](")
		if open < 0 {
			break
		}
		rest = rest[open+2:]
		close := strings.Index(rest, ")")
		if close < 0 {
			break
		}
		out = append(out, normaliseAssistantHref(cutHrefTitle(rest[:close])))
		rest = rest[close+1:]
	}
	return out
}

// normaliseApostrophes maps curly apostrophes/quotes to a plain ' so a
// model that writes "can't" with a typographic apostrophe still matches a
// forbid_any/expect_text_any phrase written with a plain one.
func normaliseApostrophes(s string) string {
	return strings.NewReplacer("’", "'", "‘", "'").Replace(s)
}

func containsAnyFold(haystack string, needles []string) bool {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// containsAny reports whether any answer href is one of the expected ones;
// an anchor or a more specific tab on the same screen counts
// (/e/x/manage?s=judging#jg-lockzone and /e/x/manage?s=teams&tab=solo
// match an expected /e/x/manage?s=judging and /e/x/manage?s=teams).
func containsAny(haystack []string, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n || (!strings.Contains(n, "#") && (strings.HasPrefix(h, n+"#") || strings.HasPrefix(h, n+"&"))) {
				return true
			}
		}
	}
	return false
}

// containsWholeWordAny is expect_text_any's matcher: a whole-word/whole-
// number match (\b-bounded), not a bare substring. "How many teams" wants
// \b1\b or \bone\b to match "There is 1 team" but not "There are 10
// teams", and "\bone\b" must not match inside "none".
func containsWholeWordAny(haystack string, needles []string) bool {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		pattern := `\b` + regexp.QuoteMeta(strings.ToLower(n)) + `\b`
		if regexp.MustCompile(pattern).MatchString(lower) {
			return true
		}
	}
	return false
}

// isUnsureAnswer: the assistant fell back to "I'm not sure" -- it opens
// with it, the way the prompt asks it to abstain. A confident claim with a
// hedge tacked on ("you can have 99 tracks, but I'm not sure") is graded
// as the claim it is.
func isUnsureAnswer(answer string) bool {
	lead := strings.ToLower(normaliseApostrophes(strings.TrimLeft(answer, " \t\n*_>#-`\"'")))
	for _, p := range []string{"i'm not sure", "i am not sure", "not sure", "i'm unsure", "i am unsure", "i don't know", "i do not know", "sorry, i'm not sure", "sorry, i don't know"} {
		if strings.HasPrefix(lead, p) {
			return true
		}
	}
	return false
}

// firstWord is the answer's first word, lower-cased, markdown and
// punctuation stripped ("**No,** you can't" -> "no").
func firstWord(answer string) string {
	s := strings.TrimLeft(answer, " \t\n*_>#-`\"'")
	end := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
	if end < 0 {
		end = len(s)
	}
	return strings.ToLower(s[:end])
}

// matchesPhrase: a number matches whole-word ("8" not inside "18"); other
// phrases match as a case-insensitive substring ("invit" matches "invite").
func matchesPhrase(lowerAnswer, phrase string) bool {
	p := strings.ToLower(phrase)
	if regexp.MustCompile(`^[0-9]+$`).MatchString(p) {
		return regexp.MustCompile(`\b` + p + `\b`).MatchString(lowerAnswer)
	}
	return strings.Contains(lowerAnswer, p)
}

// gradeAssistantAnswer is the deterministic grader. It returns pass, the
// reason, and whether the answer fell back to "not sure". A wrong answer
// (a contradiction, the opposite yes/no, a made-up link, a missing fact or
// link) fails. "I'm not sure" with a link on a question the knowledge
// answers passes the case but is counted (assistantEvalRunOK caps the
// rate); on a deliberately unanswerable question it is the only pass.
func gradeAssistantAnswer(c assistantEvalCase, answer string, validHrefs map[string]bool) (pass bool, reason string, unsure bool) {
	answer = normaliseApostrophes(answer)
	lower := strings.ToLower(answer)
	unsure = isUnsureAnswer(answer)
	if strings.TrimSpace(answer) == "" {
		return false, "empty answer", false
	}
	if containsAnyFold(answer, c.ForbidAny) {
		return false, "contains a forbidden phrase", unsure
	}
	hrefs := answerHrefs(answer)
	for _, h := range hrefs {
		if !validHrefs[h] {
			return false, fmt.Sprintf("used a made-up link: %s", h), unsure
		}
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(answer), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	if lines > c.maxLines() {
		return false, fmt.Sprintf("%d lines, max %d", lines, c.maxLines()), unsure
	}
	if !c.ExpectUnsure && len(hrefs) == 0 {
		return false, "no link (every answer ends with a link to the place to act)", unsure
	}
	if c.ExpectUnsure {
		if unsure {
			return true, "unsure, as it should be", true
		}
		return false, "answered a question it has no facts for", false
	}
	if c.ExpectStart != "" {
		if fw := firstWord(answer); fw == "yes" || fw == "no" {
			if fw != c.ExpectStart {
				return false, fmt.Sprintf("answered %q, want %q", fw, c.ExpectStart), unsure
			}
		}
	}
	if unsure && len(hrefs) > 0 {
		return true, "unsure + link", true
	}
	if c.ExpectStart != "" && firstWord(answer) != c.ExpectStart {
		return false, fmt.Sprintf("does not start with %q", c.ExpectStart), unsure
	}
	if len(c.ExpectHrefAny) > 0 && !containsAny(hrefs, c.ExpectHrefAny) {
		return false, fmt.Sprintf("missing an expected href (one of %v)", c.ExpectHrefAny), unsure
	}
	for _, group := range c.ExpectAll {
		ok := false
		for _, p := range group {
			if matchesPhrase(lower, p) {
				ok = true
				break
			}
		}
		if !ok {
			return false, fmt.Sprintf("missing an expected fact (one of %v)", group), unsure
		}
	}
	if len(c.ExpectTextAny) > 0 && !containsWholeWordAny(answer, c.ExpectTextAny) {
		return false, fmt.Sprintf("missing an expected fact (one of %v)", c.ExpectTextAny), unsure
	}
	if len(c.ExpectHrefAny) == 0 && len(c.ExpectAll) == 0 && len(c.ExpectTextAny) == 0 && c.ExpectStart == "" {
		return false, "no expectation configured", unsure
	}
	return true, "matched expectation", unsure
}

// assistantEvalMaxUnsureRate caps how many answerable questions may fall
// back to "I'm not sure" in a run (owner, 2026-11-09: with full coverage it
// should be rare). Deliberately unanswerable questions don't count.
const assistantEvalMaxUnsureRate = 0.05

// assistantEvalRunOK: every case passed, and "not sure" on answerable
// questions stayed under the cap.
func assistantEvalRunOK(perCasePass []bool, answerable, unsureAnswerable int) bool {
	for _, pass := range perCasePass {
		if !pass {
			return false
		}
	}
	return answerable == 0 || float64(unsureAnswerable) <= assistantEvalMaxUnsureRate*float64(answerable)
}

// TestGradeAssistantAnswer is a fast, network-free unit test of the grader.
func TestGradeAssistantAnswer(t *testing.T) {
	valid := map[string]bool{"/e/demo/manage?s=overview": true, "/docs/start/": true, "/e/demo/manage?s=teams&tab=solo": true, "/e/demo/manage?s=teams": true}
	home := []string{"/e/demo/manage?s=overview"}
	cases := []struct {
		name   string
		c      assistantEvalCase
		answer string
		want   bool
	}{
		{"matches expected href", assistantEvalCase{ExpectHrefAny: home}, "See [Home](/e/demo/manage?s=overview).", true},
		{"made-up link fails", assistantEvalCase{ExpectHrefAny: home}, "See [Home](/e/demo/made-up).", false},
		{"unsure with link passes an answerable case (counted)", assistantEvalCase{ExpectHrefAny: home}, "I'm not sure. See [docs](/docs/start/).", true},
		{"unsure without any link fails", assistantEvalCase{ExpectHrefAny: home}, "I'm not sure.", false},
		{"unanswerable answered fails", assistantEvalCase{ExpectUnsure: true}, "Team Rocket won.", false},
		{"unanswerable unsure passes", assistantEvalCase{ExpectUnsure: true}, "I'm not sure.", true},
		{"no expectation fails", assistantEvalCase{}, "Yes, definitely.", false},
		{"forbidden phrase fails", assistantEvalCase{ExpectHrefAny: home, ForbidAny: []string{"i've opened"}}, "I've opened it. See [Home](/e/demo/manage?s=overview).", false},
		{"too many lines fails", assistantEvalCase{ExpectHrefAny: home}, "a\nb\nc\nd\ne\n[Home](/e/demo/manage?s=overview)", false},
		{"expected text present", assistantEvalCase{ExpectTextAny: []string{"open"}}, "Your event is in the Open stage. [Home](/e/demo/manage?s=overview)", true},
		{"an answerable case without a link fails", assistantEvalCase{ExpectTextAny: []string{"open"}}, "Your event is in the Open stage.", false},
		{"a hedge after a confident claim is not an abstention", assistantEvalCase{ExpectAll: [][]string{{"12"}}}, "You can have 99 tracks, but I'm not sure. [Home](/e/demo/manage?s=overview)", false},
		{"10 teams fails the team-count case", assistantEvalCase{ExpectTextAny: []string{"1", "one"}}, "There are 10 teams. [Home](/e/demo/manage?s=overview)", false},
		{"\"one\" must not match inside \"none\"", assistantEvalCase{ExpectTextAny: []string{"one"}}, "There are none yet. [Home](/e/demo/manage?s=overview)", false},
		{"curly apostrophe forbidden phrase fails", assistantEvalCase{ExpectHrefAny: home, ForbidAny: []string{"can't see"}}, "Teams can’t see it. [Home](/e/demo/manage?s=overview)", false},
		{"link title is stripped", assistantEvalCase{ExpectHrefAny: home}, `See [Home](/e/demo/manage?s=overview "Home page").`, true},
		{"yes when no expected fails", assistantEvalCase{ExpectStart: "no", ExpectHrefAny: home}, "Yes, you can. [Home](/e/demo/manage?s=overview)", false},
		{"bold No passes", assistantEvalCase{ExpectStart: "no", ExpectHrefAny: home}, "**No,** it stays. [Home](/e/demo/manage?s=overview)", true},
		{"hedged opener fails a yes/no case", assistantEvalCase{ExpectStart: "no", ExpectHrefAny: home}, "It depends. [Home](/e/demo/manage?s=overview)", false},
		{"unsure cannot hide an opposite yes/no", assistantEvalCase{ExpectStart: "no", ExpectHrefAny: home}, "Yes, but I'm not sure. [Home](/e/demo/manage?s=overview)", false},
		{"all groups must match", assistantEvalCase{ExpectAll: [][]string{{"entry"}, {"site"}}}, "It removes the entry. [Home](/e/demo/manage?s=overview)", false},
		{"all groups matched", assistantEvalCase{ExpectAll: [][]string{{"entry"}, {"site"}}}, "It removes the entry and the site. [Home](/e/demo/manage?s=overview)", true},
		{"number matches whole word only", assistantEvalCase{ExpectAll: [][]string{{"8"}}}, "Up to 18 questions. [Home](/e/demo/manage?s=overview)", false},
		{"a more specific tab of the expected screen counts", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=teams"}}, "[Not on a team](/e/demo/manage?s=teams&tab=solo)", true},
		{"a different screen sharing a prefix does not count", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=team"}}, "[Teams](/e/demo/manage?s=teams)", false},
		{"substring phrase", assistantEvalCase{ExpectAll: [][]string{{"invit"}}}, "Create an invitation. [Home](/e/demo/manage?s=overview)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, _ := gradeAssistantAnswer(tc.c, tc.answer, valid)
			if got != tc.want {
				t.Fatalf("got pass=%v (%s), want %v", got, reason, tc.want)
			}
		})
	}
}

// TestAssistantEvalRunCapsUnsure: a model that answers "I'm not sure" to
// answerable questions fails the run past the cap, even though each such
// case passes on its own.
func TestAssistantEvalRunCapsUnsure(t *testing.T) {
	pass := make([]bool, 40)
	for i := range pass {
		pass[i] = true
	}
	if !assistantEvalRunOK(pass, 40, 2) {
		t.Fatal("2 of 40 unsure should pass")
	}
	if assistantEvalRunOK(pass, 40, 3) {
		t.Fatal("3 of 40 unsure should fail")
	}
	pass[3] = false
	if assistantEvalRunOK(pass, 40, 0) {
		t.Fatal("a failed case should fail the run")
	}
}

// evalDocsText used to duplicate cmd/server/assistant_docs.go's extraction
// outright. SIM-394 L22: that extraction now lives in one importable place
// both callers share, AssistantDocsText (assistant_docs.go, this package),
// so the eval tests the exact production prompt rather than a second,
// maybe-drifted copy of the same logic.
func evalDocsText() string {
	return AssistantDocsText(webassets.DocsTree(true))
}

// assistantEvalFixture is one event the eval asks questions about.
type assistantEvalFixture struct {
	slug  string
	org   person
	valid map[string]bool
}

// buildAssistantEvalFixtures makes the four events: "open" (the original
// fixture: Open, a track, one team, judging closes 15 December, the
// "Video, website and code link" preset), "draft" (just created),
// "judging" (two teams with entries, one judge, judging locked) and
// "ended" (a participant, then ended). Each has its own organiser.
func buildAssistantEvalFixtures(t *testing.T, a *hackApp) map[string]assistantEvalFixture {
	t.Helper()
	canJudge := events.AbilitiesForRole("organiser").Judge
	fx := map[string]assistantEvalFixture{}
	mk := func(name string, org person, slug string) {
		fx[name] = assistantEvalFixture{slug: slug, org: org, valid: assistantValidHrefs(t, slug, canJudge)}
	}

	// open
	org := a.newPerson(t, "ev")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create event: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", "/v1/hack/events/"+slug+"/entry-rules", map[string]any{"preset": "video_website_code"}, a.key(org)); r.status != 200 {
		t.Fatalf("set entry rules: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"judging_ends_at": "2026-12-15"}, a.key(org)); r.status != 200 {
		t.Fatalf("set judging_ends_at: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", "/v1/hack/events/"+slug+"/tracks", map[string]any{
		"tracks": []map[string]string{{"name": "AI track", "slug": "ai"}},
	}, a.key(org)); r.status != 200 {
		t.Fatalf("set tracks: %d %s", r.status, r.body)
	}
	part := a.newPerson(t, "pt")
	if r := a.at(t, "POST", "/v1/hack/join/"+eventJoinCode(t, a, org, slug), map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(part)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Team A"}, a.key(part)); r.status != http.StatusCreated {
		t.Fatalf("create team: %d %s", r.status, r.body)
	}
	mk("open", org, slug)

	// draft
	org = a.newPerson(t, "ed")
	slug = uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create draft event: %d %s", r.status, r.body)
	}
	mk("draft", org, slug)

	// judging, locked
	w := newJudgingWorldApp(t, a, 2, 1)
	w.setStage(t, "judging")
	if r := w.call(t, "POST", "/stage", map[string]any{"stage": "results"}, w.org); r.status != 200 {
		t.Fatalf("lock judging: %d %s", r.status, r.body)
	}
	mk("judging", w.org, w.slug)

	// ended
	org = a.newPerson(t, "ee")
	slug = uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create ended event: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	part = a.newPerson(t, "pe")
	if r := a.at(t, "POST", "/v1/hack/join/"+eventJoinCode(t, a, org, slug), map[string]any{"accept_coc": true, "display_name": "Sam"}, a.key(part)); r.status != 200 {
		t.Fatalf("join ended: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, a.key(org)); r.status != 200 {
		t.Fatalf("end event: %d %s", r.status, r.body)
	}
	mk("ended", org, slug)
	return fx
}

// assistantEvalOutcome is one case's result over its tries.
type assistantEvalOutcome struct {
	c       assistantEvalCase
	pass    bool
	notRun  bool
	reason  string
	answers []string
	tries   int
	unsure  int // tries that fell back to "not sure"
}

// TestAssistantEval: the owner's eval set of real organiser questions,
// run against the real configured model (never a fake) -- skipped unless
// ASSISTANT_EVAL=1. Every graded answer counts: one wrong answer fails its
// case and the build (owner, 2026-11-09). Only a provider error, an empty
// stream or an HTTP error is retried, and never graded; a case that never
// got an answer is "not run", which fails the run too but is reported apart
// from wrong answers. ASSISTANT_EVAL_ONLY=<substring> runs only matching
// questions.
func TestAssistantEval(t *testing.T) {
	if os.Getenv("ASSISTANT_EVAL") != "1" {
		t.Skip("set ASSISTANT_EVAL=1 to run the real organiser-assistant eval set (scripts/check-hack-assistant-eval.sh)")
	}
	baseURL := strings.TrimSpace(os.Getenv("ASSISTANT_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("ASSISTANT_MODEL"))
	if baseURL == "" || model == "" {
		t.Skip("ASSISTANT_BASE_URL and ASSISTANT_MODEL must be set to run the eval")
	}

	a := newHackApp(t)
	fixtures := buildAssistantEvalFixtures(t, a)
	llmClient := llm.New(llm.Config{
		BaseURL:         baseURL,
		Model:           model,
		APIKey:          os.Getenv("ASSISTANT_API_KEY"),
		ReasoningEffort: os.Getenv("ASSISTANT_REASONING_EFFORT"),
		MaxTokens:       events.AssistantMaxTokens,
	})
	a.hack.SetAssistant(events.NewAssistant(a.database, llmClient, events.AssistantConfig{
		BaseURL:               baseURL,
		Model:                 model,
		APIKey:                os.Getenv("ASSISTANT_API_KEY"),
		ReasoningEffort:       os.Getenv("ASSISTANT_REASONING_EFFORT"),
		MonthlyBudgetUSD:      1000, // this run's own cost accounting is not under test
		DailyQuestions:        100000,
		PriceInputPerMTokUSD:  0.5,
		PriceOutputPerMTokUSD: 2.0,
	}, evalDocsText))
	// The per-account abuse limiter (burst 40) is not under test either.
	a.hack.assistantWrites = httpx.NewRateLimiter(1e6, 1e6)

	only := os.Getenv("ASSISTANT_EVAL_ONLY")
	var cases []assistantEvalCase
	for _, c := range loadAssistantEvalCases(t) {
		if only == "" || strings.Contains(strings.ToLower(c.Question), strings.ToLower(only)) {
			if _, ok := fixtures[c.fixture()]; !ok {
				t.Fatalf("case %q: unknown fixture %q", c.Question, c.fixture())
			}
			cases = append(cases, c)
		}
	}

	if len(cases) == 0 {
		t.Fatalf("no eval cases selected (ASSISTANT_EVAL_ONLY=%q)", only)
	}

	// ask returns the answer, or ok=false for a transport/provider problem.
	ask := func(t *testing.T, fx assistantEvalFixture, c assistantEvalCase) (string, bool, string) {
		r := a.at(t, "POST", "/v1/hack/events/"+fx.slug+"/assistant", map[string]any{
			"messages": []map[string]string{{"role": "user", "content": c.Question}},
			"screen":   c.Screen,
			"tab":      c.Tab,
		}, a.key(fx.org))
		if r.status != 200 {
			return "", false, fmt.Sprintf("HTTP %d", r.status)
		}
		var answer strings.Builder
		for _, ev := range readSSE(t, r.body) {
			if d, ok := ev["delta"].(string); ok {
				answer.WriteString(d)
			}
			if code, ok := ev["error"].(string); ok {
				return "", false, "provider " + code
			}
		}
		if strings.TrimSpace(answer.String()) == "" {
			return "", false, "empty answer"
		}
		return answer.String(), true, ""
	}

	outcomes := make([]assistantEvalOutcome, len(cases))
	var mu sync.Mutex
	t.Run("cases", func(t *testing.T) {
		for i, c := range cases {
			i, c := i, c
			t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
				t.Parallel()
				fx := fixtures[c.fixture()]
				cc := c.withSlug(fx.slug)
				out := assistantEvalOutcome{c: cc}
				graded := []bool{}
				transport := 0
				for len(graded) < 1 && transport < 5 {
					answer, ok, why := ask(t, fx, cc)
					if !ok {
						transport++
						out.reason = why
						time.Sleep(time.Duration(transport) * 2 * time.Second)
						continue
					}
					pass, reason, unsure := gradeAssistantAnswer(cc, answer, fx.valid)
					out.tries++
					if unsure {
						out.unsure++
					}
					out.answers = append(out.answers, answer)
					graded = append(graded, pass)
					if !pass || len(graded) == 1 {
						out.reason = reason
					}
				}
				switch {
				case len(graded) == 0:
					out.notRun = true
				case graded[0]:
					out.pass = true
				}
				mu.Lock()
				outcomes[i] = out
				mu.Unlock()
			})
		}
	})

	var report *os.File
	if path := os.Getenv("ASSISTANT_EVAL_REPORT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("open ASSISTANT_EVAL_REPORT %s: %v", path, err)
		}
		defer f.Close()
		report = f
	}
	write := func(s string) {
		fmt.Print(s)
		if report != nil {
			_, _ = report.WriteString(s)
		}
	}

	type areaStat struct{ cases, wrong, notRun, unsureCases int }
	areas := map[string]*areaStat{}
	var perCasePass []bool
	wrong, notRun, answerable, unsureAnswerable, tries, unsureTries := 0, 0, 0, 0, 0, 0
	for _, o := range outcomes {
		st := areas[o.c.Area]
		if st == nil {
			st = &areaStat{}
			areas[o.c.Area] = st
		}
		st.cases++
		status := "PASS"
		switch {
		case o.notRun:
			status, notRun = "NOT RUN", notRun+1
			st.notRun++
		case !o.pass:
			status, wrong = "FAIL", wrong+1
			st.wrong++
		}
		perCasePass = append(perCasePass, o.pass)
		tries += o.tries
		unsureTries += o.unsure
		// A case counts as "not sure" when any of its graded answers was,
		// so a retry can't launder a fallback into a pass.
		if !o.c.ExpectUnsure {
			answerable++
			if o.unsure > 0 {
				unsureAnswerable++
				st.unsureCases++
			}
		}
		last := ""
		if len(o.answers) > 0 {
			last = o.answers[len(o.answers)-1]
		}
		write(fmt.Sprintf("[%s] (%s/%s) %q\n  answer: %s\n  reason: %s (tries %d)\n", status, o.c.Area, o.c.fixture(), o.c.Question, oneLine(last), o.reason, o.tries))
	}
	names := make([]string, 0, len(areas))
	for n := range areas {
		names = append(names, n)
	}
	sort.Strings(names)
	write("\n== by area: cases / wrong / not run / not sure ==\n")
	for _, n := range names {
		st := areas[n]
		write(fmt.Sprintf("%-18s %3d %3d %3d %3d\n", n, st.cases, st.wrong, st.notRun, st.unsureCases))
	}
	rate := 0.0
	if answerable > 0 {
		rate = float64(unsureAnswerable) / float64(answerable)
	}
	triesRate := 0.0
	if tries > 0 {
		triesRate = float64(unsureTries) / float64(tries)
	}
	write(fmt.Sprintf("\nSUMMARY cases=%d wrong=%d not_run=%d not_sure_answerable=%d/%d (%.1f%%) not_sure_all_tries=%d/%d (%.1f%%)\n",
		len(outcomes), wrong, notRun, unsureAnswerable, answerable, 100*rate, unsureTries, tries, 100*triesRate))
	if !assistantEvalRunOK(perCasePass, answerable, unsureAnswerable) {
		t.Fatalf("%d wrong, %d not run, not sure on %d of %d answerable questions (cap %.0f%%) -- see the report", wrong, notRun, unsureAnswerable, answerable, 100*assistantEvalMaxUnsureRate)
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// eventJoinCode reads join_code from the organiser's own event view
// (only ever used by test fixtures -- the real assistant sanitiser strips
// this field for exactly that reason).
func eventJoinCode(t *testing.T, a *hackApp, org person, slug string) string {
	t.Helper()
	ev := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)
	orgView, ok := ev["organiser"].(map[string]any)
	if !ok {
		t.Fatalf("missing organiser view: %v", ev)
	}
	code, _ := orgView["join_code"].(string)
	if code == "" {
		t.Fatalf("missing join_code: %v", orgView)
	}
	return code
}
