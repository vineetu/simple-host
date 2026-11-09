package hackhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/llm"
	"github.com/vsriram/simple-host/internal/webassets"
)

// assistantEvalCase is one row of testdata/assistant_eval.json.
type assistantEvalCase struct {
	Question      string   `json:"question"`
	ExpectHrefAny []string `json:"expect_href_any,omitempty"`
	ExpectTextAny []string `json:"expect_text_any,omitempty"`
	AllowUnsure   *bool    `json:"allow_unsure,omitempty"`
	ForbidAny     []string `json:"forbid_any,omitempty"`
	MaxLines      int      `json:"max_lines,omitempty"`
}

func (c assistantEvalCase) allowUnsure() bool {
	if c.AllowUnsure == nil {
		return true
	}
	return *c.AllowUnsure
}

func (c assistantEvalCase) maxLines() int {
	if c.MaxLines <= 0 {
		return 3
	}
	return c.MaxLines
}

func loadAssistantEvalCases(t *testing.T, slug string) []assistantEvalCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/assistant_eval.json")
	if err != nil {
		t.Fatalf("read assistant_eval.json: %v", err)
	}
	var cases []assistantEvalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse assistant_eval.json: %v", err)
	}
	for i := range cases {
		for j, h := range cases[i].ExpectHrefAny {
			cases[i].ExpectHrefAny[j] = strings.ReplaceAll(h, "{slug}", slug)
		}
	}
	return cases
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
// an anchor on the same screen counts (/e/x/manage?s=judging#jg-lockzone
// matches an expected /e/x/manage?s=judging).
func containsAny(haystack []string, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n || (!strings.Contains(n, "#") && strings.HasPrefix(h, n+"#")) {
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

// gradeAssistantAnswer is the deterministic grader (owner requirement: a
// wrong answer fails the build, "I'm not sure + link" is a pass). It never
// calls a model; it only judges a given answer text. Kept separate from
// TestAssistantEval so it has its own fast, network-free unit test.
func gradeAssistantAnswer(c assistantEvalCase, answer string, validHrefs map[string]bool) (pass bool, reason string) {
	answer = normaliseApostrophes(answer)
	if containsAnyFold(answer, c.ForbidAny) {
		return false, "contains a forbidden phrase"
	}
	hrefs := answerHrefs(answer)
	for _, h := range hrefs {
		if !validHrefs[h] {
			return false, fmt.Sprintf("used a made-up link: %s", h)
		}
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(answer), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	if lines > c.maxLines() {
		return false, fmt.Sprintf("%d lines, max %d", lines, c.maxLines())
	}

	if c.allowUnsure() && containsAnyFold(answer, []string{"not sure", "don't know", "do not know", "unsure"}) && len(hrefs) > 0 {
		return true, "unsure + link"
	}
	hasHrefReq := len(c.ExpectHrefAny) > 0
	hasTextReq := len(c.ExpectTextAny) > 0
	if !hasHrefReq && !hasTextReq {
		return false, "no expectation configured and the answer did not say \"not sure\" with a link"
	}
	if hasHrefReq && !containsAny(hrefs, c.ExpectHrefAny) {
		return false, fmt.Sprintf("missing an expected href (one of %v)", c.ExpectHrefAny)
	}
	if hasTextReq && !containsWholeWordAny(answer, c.ExpectTextAny) {
		return false, fmt.Sprintf("missing an expected fact (one of %v)", c.ExpectTextAny)
	}
	return true, "matched expectation"
}

// assistantEvalRunOK is the whole-run honesty gate (SIM-394 H3): every
// case must grade true, AND at most assistantEvalMaxUnsure of them may pass via the "unsure +
// link" path. Without the second half, a model that answers "I'm not
// sure" to everything -- including questions the event data already
// answers -- would pass every case that allows the unsure path, which
// defeats the eval's purpose.
// assistantEvalMaxUnsure: the questions whose facts are in the event set
// allow_unsure false, so this cap only bounds the rest (18 cases, 2026-11-09).
const assistantEvalMaxUnsure = 5

func assistantEvalRunOK(perCasePass []bool, unsureCount int) bool {
	if unsureCount > assistantEvalMaxUnsure {
		return false
	}
	for _, pass := range perCasePass {
		if !pass {
			return false
		}
	}
	return true
}

// TestGradeAssistantAnswer is a fast, network-free unit test of the grader
// above (always runs; no DB_DSN, no ASSISTANT_EVAL needed).
func TestGradeAssistantAnswer(t *testing.T) {
	valid := map[string]bool{"/e/demo/manage?s=overview": true, "/docs/start/": true}
	cases := []struct {
		name   string
		c      assistantEvalCase
		answer string
		want   bool
	}{
		{"matches expected href", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}}, "See [Home](/e/demo/manage?s=overview).", true},
		{"made-up link fails", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}}, "See [Home](/e/demo/made-up).", false},
		{"unsure with link passes", assistantEvalCase{}, "I'm not sure. See [docs](/docs/start/).", true},
		{"unsure without any link fails", assistantEvalCase{}, "I'm not sure.", false},
		{"no expectation, confident answer fails", assistantEvalCase{}, "Yes, definitely.", false},
		{"forbidden phrase fails", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}, ForbidAny: []string{"i've opened"}}, "I've opened it. See [Home](/e/demo/manage?s=overview).", false},
		{"too many lines fails", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}}, "a\nb\nc\nd\n[Home](/e/demo/manage?s=overview)", false},
		{"expected text present", assistantEvalCase{ExpectTextAny: []string{"open"}}, "Your event is in the Open stage.", true},
		{"expected text missing", assistantEvalCase{ExpectTextAny: []string{"open"}}, "Your event is in the Draft stage.", false},
		{"max_lines override", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}, MaxLines: 15}, strings.Repeat("line\n", 10) + "[Home](/e/demo/manage?s=overview)", true},
		// SIM-394 H3: whole-word/number matching for expect_text_any --
		// "10 teams" must not satisfy a case expecting the literal count 1.
		{"10 teams fails the team-count case", assistantEvalCase{ExpectTextAny: []string{"1", "one"}}, "There are 10 teams.", false},
		{"1 team passes the team-count case", assistantEvalCase{ExpectTextAny: []string{"1", "one"}}, "There is 1 team.", true},
		{"\"one\" must not match inside \"none\"", assistantEvalCase{ExpectTextAny: []string{"one"}}, "There are none yet.", false},
		// SIM-394 H3: a curly apostrophe in the model's answer still trips
		// a forbid_any phrase written with a plain apostrophe.
		{"curly apostrophe forbidden phrase fails", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}, ForbidAny: []string{"can't see"}}, "Teams can’t see it. [Home](/e/demo/manage?s=overview)", false},
		// SIM-394 H3: a markdown link title is cut off before the href is
		// checked against validHrefs.
		{"link title is stripped before matching", assistantEvalCase{ExpectHrefAny: []string{"/e/demo/manage?s=overview"}}, `See [Home](/e/demo/manage?s=overview "Home page").`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := gradeAssistantAnswer(tc.c, tc.answer, valid)
			if got != tc.want {
				t.Fatalf("got pass=%v (%s), want %v", got, reason, tc.want)
			}
		})
	}
}

// TestAssistantEvalRunFailsOnTooManyUnsure: a model that answers "I'm not
// sure" to every question -- even ones the event data already answers --
// must fail the whole eval run, not pass case-by-case (SIM-394 H3). This
// is the pure, network-free run-level counterpart to TestGradeAssistantAnswer
// above; TestAssistantEval itself exercises assistantEvalRunOK for real
// against the configured model.
func TestAssistantEvalRunFailsOnTooManyUnsure(t *testing.T) {
	valid := map[string]bool{"/docs/start/": true}
	answer := "I'm not sure. See [docs](/docs/start/)."
	run := func(n int, last string) bool {
		var perCasePass []bool
		unsureCount := 0
		for i := 0; i < n; i++ {
			c, a := assistantEvalCase{Question: fmt.Sprint(i)}, answer
			if i == n-1 && last != "" {
				c, a = assistantEvalCase{Question: "last", ExpectTextAny: []string{"four"}}, last
			}
			pass, reason := gradeAssistantAnswer(c, a, valid)
			perCasePass = append(perCasePass, pass)
			if pass && reason == "unsure + link" {
				unsureCount++
			}
		}
		return assistantEvalRunOK(perCasePass, unsureCount)
	}
	if run(assistantEvalMaxUnsure+1, "") {
		t.Fatalf("expected the run to fail: %d cases passed via the unsure path", assistantEvalMaxUnsure+1)
	}
	if !run(assistantEvalMaxUnsure+1, "It is four.") {
		t.Fatalf("expected the run to pass: only %d cases passed via the unsure path", assistantEvalMaxUnsure)
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

// TestAssistantEval: the owner's requirement for an eval set of real
// organiser questions with known right answers, run against the real
// configured model (never a fake) -- skipped unless ASSISTANT_EVAL=1, and
// again if the provider isn't actually configured. The lead runs this with
// the real key (scripts/check-hack-assistant-eval.sh); this session never
// sets ASSISTANT_EVAL=1 itself.
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
	org := a.newPerson(t, "ev")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create event: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug) // stage: open

	// A known judging-close date (fact for "when does judging close?").
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"judging_ends_at": "2026-12-15"}, a.key(org)); r.status != 200 {
		t.Fatalf("set judging_ends_at: %d %s", r.status, r.body)
	}
	// A known track (fact for "where are tracks?").
	if r := a.at(t, "PUT", "/v1/hack/events/"+slug+"/tracks", map[string]any{
		"tracks": []map[string]string{{"name": "AI track", "slug": "ai"}},
	}, a.key(org)); r.status != 200 {
		t.Fatalf("set tracks: %d %s", r.status, r.body)
	}
	// createEvent already seeds the default rubric (Idea/Execution/Design/
	// Demo, weights summing to 100) -- a real rubric for "how do I change
	// the rubric?" and "can participants see the rubric?" without needing
	// a separate PUT.

	// A known team count (fact for "how many teams are there?").
	part := a.newPerson(t, "pt")
	if r := a.at(t, "POST", "/v1/hack/join/"+eventJoinCode(t, a, org, slug), map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(part)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Team A"}, a.key(part)); r.status != http.StatusCreated {
		t.Fatalf("create team: %d %s", r.status, r.body)
	}

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
		MonthlyBudgetUSD:      1000, // generous: this run's own cost accounting is not what is under test
		DailyQuestions:        1000,
		PriceInputPerMTokUSD:  0.5,
		PriceOutputPerMTokUSD: 2.0,
	}, evalDocsText))

	// org's membership role is "organiser" (createEvent made it so), and
	// canJudge here must use the exact same rule buildSystemPrompt's
	// PLACES section gates judge targets on -- events.AbilitiesForRole
	// mirrors events.MembershipAbilities(member).Judge for a joined
	// member, which is what hack_assistant.go actually passes to Ask.
	canJudge := events.AbilitiesForRole("organiser").Judge
	validHrefs := assistantValidHrefs(t, slug, canJudge)
	cases := loadAssistantEvalCases(t, slug)

	// ASSISTANT_EVAL_REPORT: a side-channel file for the per-case lines.
	// go test -json (scripts/test-integration.sh's own summarizer) only
	// ever prints a FAILING test's captured stdout, so on a fully passing
	// run the per-case question/answer/pass lines below would otherwise
	// never reach anyone; scripts/check-hack-assistant-eval.sh sets this
	// and cats it regardless of the exit code.
	var report *os.File
	if path := os.Getenv("ASSISTANT_EVAL_REPORT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("open ASSISTANT_EVAL_REPORT %s: %v", path, err)
		}
		defer f.Close()
		report = f
	}

	failures := 0
	unsureCount := 0
	var perCasePass []bool
	for _, c := range cases {
		r := a.at(t, "POST", "/v1/hack/events/"+slug+"/assistant", map[string]any{
			"messages": []map[string]string{{"role": "user", "content": c.Question}},
		}, a.key(org))
		var answer strings.Builder
		for _, ev := range readSSE(t, r.body) {
			if d, ok := ev["delta"].(string); ok {
				answer.WriteString(d)
			}
			if errCode, ok := ev["error"].(string); ok {
				t.Logf("case %q: provider error mid-stream: %s", c.Question, errCode)
			}
		}
		pass, reason := gradeAssistantAnswer(c, answer.String(), validHrefs)
		perCasePass = append(perCasePass, pass)
		if pass && reason == "unsure + link" {
			unsureCount++
		}
		status := "PASS"
		if !pass {
			status = "FAIL"
			failures++
		}
		line := fmt.Sprintf("[%s] %q\n  answer: %s\n  reason: %s\n", status, c.Question, oneLine(answer.String()), reason)
		fmt.Print(line)
		if report != nil {
			_, _ = report.WriteString(line)
		}
	}
	// SIM-394 H3: more than 3 cases passing only via "I'm not sure + link"
	// fails the whole run, even if every individual case otherwise graded
	// true -- the assistant must not dodge mostly-answerable questions.
	if unsureCount > assistantEvalMaxUnsure {
		line := fmt.Sprintf("\n%d cases passed via the unsure path (max %d allowed) -- failing the run\n", unsureCount, assistantEvalMaxUnsure)
		fmt.Print(line)
		if report != nil {
			_, _ = report.WriteString(line)
		}
	}
	if report != nil {
		_, _ = report.WriteString(fmt.Sprintf("\n%d of %d cases failed\n", failures, len(cases)))
	}
	if !assistantEvalRunOK(perCasePass, unsureCount) {
		t.Fatalf("%d of %d eval cases failed, %d passed via the unsure path (max %d) -- see output above, or $ASSISTANT_EVAL_REPORT", failures, len(cases), unsureCount, assistantEvalMaxUnsure)
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
