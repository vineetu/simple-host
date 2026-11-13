package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/llm"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// AssistantMaxTokens is the max_tokens sent with every assistant request
// (SIM-394) and the output-side term in the worst-case reservation
// estimate below; cmd/server wires the same value into llm.Config.MaxTokens
// so the two never drift apart.
const AssistantMaxTokens = 800

// Caps on how much of the event view and the docs text are embedded in the
// system prompt (SIM-394 API contract).
const (
	assistantEventViewCapBytes = 24 * 1024
	assistantDocsCapBytes      = 16 * 1024
)

// AssistantLLM is the subset of *llm.Client's API AssistantService needs;
// tests substitute a fake.
type AssistantLLM interface {
	Stream(ctx context.Context, messages []llm.Message, onDelta func(string)) (llm.Usage, error)
}

// ChatMessage is one request message. It mirrors llm.Message's JSON shape
// exactly, but lives here (not internal/llm) so hackhttp -- which may not
// import internal/llm (scripts/layering/rules.txt: only app/hack/events may)
// -- can decode the request body straight into this type and hand it to
// Ask without ever naming the llm package.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// DocsTextFunc returns the pre-built, already-capped DOCS block (each page
// as "## [<title>](<url>)" plus its extracted text). internal/app/hack/events
// cannot import internal/webassets (scripts/layering/rules.txt), so the
// actual HTML extraction lives in internal/hackhttp (or cmd/server) and is
// injected here; that caller is responsible for computing it once
// (sync.Once) rather than re-parsing the embedded docs on every question.
type DocsTextFunc func() string

// AssistantConfig is the SIM-394 assistant's settings (internal/config
// ASSISTANT_* env vars), threaded through from cmd/server.
type AssistantConfig struct {
	BaseURL               string
	Model                 string
	APIKey                string
	ReasoningEffort       string
	MonthlyBudgetUSD      float64
	DailyQuestions        int
	PriceInputPerMTokUSD  float64
	PriceOutputPerMTokUSD float64
}

// AssistantService answers an organiser's questions about one event, built
// only from the shared navigation map, that event's own view and the Hack
// docs text -- never anything hand-written about the product. Off (every
// call behaves as disabled) unless BaseURL and Model are both set.
type AssistantService struct {
	db       *sql.DB
	llm      AssistantLLM
	cfg      AssistantConfig
	docsText DocsTextFunc
	enabled  bool
}

// NewAssistant builds the service. llmClient and docsText may be nil when
// cfg leaves the assistant off (enabled() guards every use of them).
func NewAssistant(database *sql.DB, llmClient AssistantLLM, cfg AssistantConfig, docsText DocsTextFunc) *AssistantService {
	return &AssistantService{
		db:       database,
		llm:      llmClient,
		cfg:      cfg,
		docsText: docsText,
		enabled:  strings.TrimSpace(cfg.BaseURL) != "" && strings.TrimSpace(cfg.Model) != "",
	}
}

// Enabled reports whether the assistant is configured on this instance.
func (s *AssistantService) Enabled() bool { return s.enabled }

// AssistantStatus is the GET /v1/hack/events/{slug}/assistant payload.
type AssistantStatus struct {
	Enabled        bool
	DailyLimit     int
	UsedToday      int
	RemainingToday int
	BudgetReached  bool
	// Resets is the first day of next month (UTC, YYYY-MM-DD); only
	// meaningful when BudgetReached.
	Resets string
}

// AssistantRefusal is every way Ask (or the GET status check) can refuse:
// a pre-stream condition the transport renders as a plain JSON error, or,
// when it comes back after Ask has already called onDelta at least once,
// the final SSE error event. Code is one of assistant_off, bad_request,
// daily_limit, monthly_budget, provider_error.
type AssistantRefusal struct {
	Code    string
	Message string
	// RemainingToday is set (always 0) for daily_limit; Resets (YYYY-MM-DD,
	// UTC) is set for monthly_budget. Zero value otherwise.
	RemainingToday int
	Resets         string
}

func (e *AssistantRefusal) Error() string { return e.Message }

// errAssistantEmptyAnswer is logged (never returned to the caller as-is)
// when the provider's stream succeeded but produced no non-whitespace
// content -- a distinct failure from a transport/provider error, but one
// still worth a single log line under the same "hack: assistant provider"
// prefix.
var errAssistantEmptyAnswer = errors.New("stream succeeded but produced no non-whitespace content")

func assistantDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func assistantMonth(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func assistantNextMonth(t time.Time) time.Time {
	return assistantMonth(t).AddDate(0, 1, 0)
}

func usdToMicro(usd float64) int64 {
	return int64(math.Round(usd * 1_000_000))
}

// Status builds the GET payload for userID (an organiser; the HTTP layer
// has already checked that). Independent of any one event: the daily
// count is per organiser and the budget is instance-wide.
func (s *AssistantService) Status(ctx context.Context, userID string) (AssistantStatus, error) {
	if !s.enabled {
		return AssistantStatus{Enabled: false}, nil
	}
	now := time.Now()
	used, err := eventrepo.AssistantUsedToday(ctx, s.db, userID, assistantDay(now))
	if err != nil {
		return AssistantStatus{}, err
	}
	spent, err := eventrepo.AssistantSpentThisMonth(ctx, s.db, assistantMonth(now))
	if err != nil {
		return AssistantStatus{}, err
	}
	remaining := s.cfg.DailyQuestions - used
	if remaining < 0 {
		remaining = 0
	}
	budgetReached := usdToMicro(s.cfg.MonthlyBudgetUSD)-spent < s.minReserveMicro()
	resets := ""
	if budgetReached {
		resets = assistantNextMonth(now).Format("2006-01-02")
	}
	return AssistantStatus{
		Enabled:        true,
		DailyLimit:     s.cfg.DailyQuestions,
		UsedToday:      used,
		RemainingToday: remaining,
		BudgetReached:  budgetReached,
		Resets:         resets,
	}, nil
}

// AskResult is Ask's successful outcome (the "done" SSE event's payload).
type AskResult struct {
	RemainingToday int
}

// assistantMaxTurnRunes is the per-message cap, in runes (not bytes: a
// multi-byte character like an emoji or a CJK character is one character
// to the person typing it, and must count as one here too).
// assistantMaxTotalRunes is the whole conversation's cap, counted over
// every user turn plus every assistant turn after truncation (never the
// untruncated length), the same way a caller's own running transcript
// would see it.
const (
	assistantMaxTurnRunes  = 2000
	assistantMaxTotalRunes = 12000
)

// validateAssistantMessages enforces the contract's request shape: 1..20
// messages, the last one role "user". Only user turns are rejected for
// length (1..2000 runes after trimming) -- a caller sends its own earlier
// assistant replies back for context, and those were never typed by a
// person, so rather than reject a long one this truncates it server-side
// to 2000 runes before it ever reaches the prompt. The 12000-rune total is
// counted over user turns plus truncated assistant turns (so it reflects
// what the model will actually see). Trims and (for assistant turns)
// truncates Content in place.
func validateAssistantMessages(messages []ChatMessage) *AssistantRefusal {
	if len(messages) < 1 || len(messages) > 20 {
		return &AssistantRefusal{Code: "bad_request", Message: "messages must have 1 to 20 entries"}
	}
	if messages[len(messages)-1].Role != "user" {
		return &AssistantRefusal{Code: "bad_request", Message: "the last message must have role \"user\""}
	}
	total := 0
	for i := range messages {
		messages[i].Content = strings.TrimSpace(messages[i].Content)
		switch messages[i].Role {
		case "user":
			n := utf8.RuneCountInString(messages[i].Content)
			if n < 1 || n > assistantMaxTurnRunes {
				return &AssistantRefusal{Code: "bad_request", Message: "each user message must be 1 to 2000 characters after trimming"}
			}
			total += n
		case "assistant":
			messages[i].Content = truncateRunes(messages[i].Content, assistantMaxTurnRunes)
			total += utf8.RuneCountInString(messages[i].Content)
		default:
			return &AssistantRefusal{Code: "bad_request", Message: "each message role must be \"user\" or \"assistant\""}
		}
	}
	if total > assistantMaxTotalRunes {
		return &AssistantRefusal{Code: "bad_request", Message: "the conversation is too long"}
	}
	return nil
}

// truncateRunes returns s cut to at most max runes (not bytes), leaving it
// unchanged when it is already within the limit.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

// AskContext is everything about the asking organiser's event and screen
// that the prompt is built from (organiser assistant v2, 2026-11-09).
// EventView is the already-sanitised, size-capped event-view JSON;
// Settings is the CURRENT SETTINGS block (plain lines, built by the HTTP
// layer from an allow-list). Screen and Tab are the browser's hint of where
// the organiser is asking from; unknown values are ignored.
type AskContext struct {
	Slug      string
	CanJudge  bool
	EventView json.RawMessage
	Settings  string
	Screen    string
	Tab       string
}

// buildSystemPrompt assembles the whole system prompt: the rules, the
// current screen, PLACES (every organiser link, plus judge links when
// canJudge; labels and links only -- what each place does lives in
// KNOWLEDGE), KNOWLEDGE (every record of the navigation map's explanation
// layer, navigation-help.json, with its link), CURRENT SETTINGS, EVENT
// (capped), DOCS (capped) and the current time.
func (s *AssistantService) buildSystemPrompt(in AskContext, now time.Time) string {
	var b strings.Builder
	b.WriteString(assistantPromptRules)

	nav := rules.Navigation()
	if screen := assistantScreenLabel(nav, in.Screen, in.Tab); screen != "" {
		b.WriteString("\n\nCURRENT SCREEN: ")
		b.WriteString(screen)
		b.WriteString("\n")
	}

	b.WriteString("\n\nPLACES:\n")
	seen := make(map[string]bool)
	for _, t := range nav.Targets {
		if t.Role != "organiser" && !(in.CanJudge && t.Role == "judge") {
			continue
		}
		href := strings.ReplaceAll(t.HrefPattern, "{slug}", url.PathEscape(in.Slug))
		if href == "" || strings.Contains(href, "{") {
			continue
		}
		// Several navigation targets share the same label+href: list each
		// distinct link once.
		key := t.Label + "\x00" + href
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&b, "- [%s](%s)\n", t.Label, href)
	}

	b.WriteString("\nKNOWLEDGE:\n")
	b.WriteString(assistantKnowledge(in.Slug))

	b.WriteString("\nCURRENT SETTINGS:\n")
	if strings.TrimSpace(in.Settings) == "" {
		b.WriteString("(unavailable)\n")
	} else {
		b.WriteString(in.Settings)
	}

	eventJSON := string(in.EventView)
	if len(eventJSON) > assistantEventViewCapBytes {
		eventJSON = eventJSON[:assistantEventViewCapBytes] + "...(truncated)"
	}
	b.WriteString("\nEVENT:\n")
	b.WriteString(eventJSON)

	docs := ""
	if s.docsText != nil {
		docs = s.docsText()
	}
	if len(docs) > assistantDocsCapBytes {
		docs = docs[:assistantDocsCapBytes] + "...(truncated)"
	}
	b.WriteString("\n\nDOCS:\n")
	b.WriteString(docs)

	fmt.Fprintf(&b, "\n\nNow: %s\n", now.UTC().Format(time.RFC3339))
	return b.String()
}

// assistantKnowledge renders every explanation record with its link. The
// whole layer goes in on every question (about 50 KB, 2026-11-09; the
// prompt-size guard in hack_assistant_test.go fails if a small event's
// whole prompt passes 72 KB): no per-question selection, so a question
// never misses the record it needs. Records stay whole. Rendered once with a sentinel slug, then the real
// (path-escaped) slug is swapped in per question.
func assistantKnowledge(slug string) string {
	assistantKnowledgeOnce.Do(func() {
		var b strings.Builder
		for _, r := range rules.NavigationHelp().Records {
			href := ""
			if link, err := rules.Resolve(r.Target, assistantSlugSentinel, nil); err == nil && !strings.Contains(link.Href, "{") {
				href = link.Href
			}
			b.WriteString(r.HelpText(href))
			b.WriteString("\n")
		}
		assistantKnowledgeText = b.String()
	})
	return strings.ReplaceAll(assistantKnowledgeText, assistantSlugSentinel, url.PathEscape(slug))
}

const assistantSlugSentinel = "zzassistantslugzz"

var (
	assistantKnowledgeOnce sync.Once
	assistantKnowledgeText string
)

// assistantScreenLabel names the organiser screen (and tab) the question
// was asked from, from the navigation map; "" for anything it doesn't know.
func assistantScreenLabel(nav rules.NavigationMap, screen, tab string) string {
	screen = strings.TrimSpace(screen)
	if screen == "" {
		return ""
	}
	label := ""
	for _, t := range nav.Targets {
		if t.Role == "organiser" && t.Kind == "screen" && t.Screen == screen {
			label = t.Label
			break
		}
	}
	if label == "" {
		for _, sc := range nav.Screens {
			if sc.Role == "organiser" && sc.ID == screen {
				label = sc.Section
				break
			}
		}
	}
	if label == "" {
		return ""
	}
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return label
	}
	if screen == "organiser-settings" {
		for _, st := range nav.Resolution.SettingsTabs {
			if st.ID == tab {
				return label + " > " + st.Label
			}
		}
		return label
	}
	for _, r := range []rune(tab) {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return label
		}
	}
	if len(tab) > 40 {
		return label
	}
	return label + " (tab " + tab + ")"
}

const assistantPromptRules = `You help an organiser run this one event in Simple Hack. Answer questions about what any organiser setting, option or action means and does, where it is, how to do something, and what happens if they do it.
Answer in one to three short lines unless they ask for more detail, and always end with a link to the most relevant place.
Use only the facts below. KNOWLEDGE explains every organiser screen, setting, option and action; CURRENT SETTINGS and EVENT are this event's own values; CURRENT SCREEN is where the organiser is asking from. For a yes-or-no question, start your answer with "Yes" or "No".
If the facts don't answer the question, say "I'm not sure" and link the closest place or docs page. Never guess, and never fill a gap with what other apps usually do.
You cannot change anything in Simple Hack, so never say you changed or will change something; to act, link where the organiser does it themselves.
Links: copy a whole markdown link exactly as it appears in PLACES, KNOWLEDGE or DOCS. The href always starts with /e/ or /docs/; never add a domain, a space or a second slash.
Do not use headings.`

// estimateTokens is the contract's worst-case reservation heuristic:
// roughly 3 bytes per token.
func estimateTokens(byteLen int) int {
	return byteLen / 3
}

// assistantTypicalPromptBytes is a fixed stand-in for a whole system
// prompt (rules + PLACES + KNOWLEDGE + CURRENT SETTINGS + EVENT + DOCS)
// plus a short question, used only by minReserveMicro below -- not the
// per-request estimate Ask itself reserves against (that one measures the
// real prompt and messages). Measured 2026-11-09 (organiser assistant v2):
// about 63 KB for a small event with its settings, event view and docs.
const assistantTypicalPromptBytes = 64 * 1024

// minReserveMicro is the smallest reservation a real question could need:
// a 0-byte user message plus the typical-size prompt above, priced the
// same way Ask prices every request (input tokens at the estimate
// heuristic, output tokens at the fixed AssistantMaxTokens ceiling). GET
// Status uses it so "budget reached" means the same thing there as it
// does to the next POST: once the remaining budget can no longer cover
// even a minimal question, no further question will be answered until
// the budget resets, so GET already reports that. Deliberately simple:
// the real, per-request reservation (Ask) still measures the actual
// prompt and messages; this is a gate used for display only.
func (s *AssistantService) minReserveMicro() int64 {
	estimatedInputTokens := estimateTokens(assistantTypicalPromptBytes)
	return int64(math.Round(float64(estimatedInputTokens)*s.cfg.PriceInputPerMTokUSD + float64(AssistantMaxTokens)*s.cfg.PriceOutputPerMTokUSD))
}

// Ask validates the request, reserves the worst-case cost against the
// daily and monthly limits, streams the answer through onDelta, and
// settles (or, on a provider failure before any delta, refunds) the
// reservation. in carries the event, the asker's screen and the
// already-sanitised event view and settings the HTTP layer built.
//
// Every returned error is an *AssistantRefusal except a genuine
// infrastructure failure (a database error), which the transport maps to
// a 500. onDelta is never called before Ask has committed to answering (a
// successful reservation), so a caller that has not yet written any
// response bytes may still turn an *AssistantRefusal into a plain JSON
// response; once onDelta has been called at least once, any further error
// is always Code "provider_error" and belongs in a final SSE event instead.
func (s *AssistantService) Ask(ctx context.Context, userID string, in AskContext, messages []ChatMessage, onDelta func(string)) (AskResult, error) {
	if !s.enabled {
		return AskResult{}, &AssistantRefusal{Code: "assistant_off", Message: "The assistant is not set up on this server."}
	}
	if refusal := validateAssistantMessages(messages); refusal != nil {
		return AskResult{}, refusal
	}

	now := time.Now()
	day := assistantDay(now)
	month := assistantMonth(now)

	systemPrompt := s.buildSystemPrompt(in, now)
	inputBytes := len(systemPrompt)
	for _, m := range messages {
		inputBytes += len(m.Content)
	}
	estimatedInputTokens := estimateTokens(inputBytes)
	reserveMicro := int64(math.Round(float64(estimatedInputTokens)*s.cfg.PriceInputPerMTokUSD + float64(AssistantMaxTokens)*s.cfg.PriceOutputPerMTokUSD))
	budgetMicro := usdToMicro(s.cfg.MonthlyBudgetUSD)

	if err := eventrepo.AssistantReserve(ctx, s.db, userID, day, month, s.cfg.DailyQuestions, budgetMicro, reserveMicro); err != nil {
		switch {
		case errors.Is(err, eventrepo.ErrAssistantDailyLimit):
			return AskResult{}, &AssistantRefusal{
				Code:           "daily_limit",
				Message:        fmt.Sprintf("You've asked %d questions today. The limit resets at midnight UTC.", s.cfg.DailyQuestions),
				RemainingToday: 0,
			}
		case errors.Is(err, eventrepo.ErrAssistantMonthlyBudget):
			return AskResult{}, &AssistantRefusal{
				Code:    "monthly_budget",
				Message: "The assistant has used this month's budget. It's back on " + assistantNextMonth(now).Format("2 January") + ".",
				Resets:  assistantNextMonth(now).Format("2006-01-02"),
			}
		default:
			return AskResult{}, err
		}
	}

	full := make([]llm.Message, 0, len(messages)+1)
	full = append(full, llm.Message{Role: "system", Content: systemPrompt})
	for _, m := range messages {
		full = append(full, llm.Message{Role: m.Role, Content: m.Content})
	}

	var answer strings.Builder
	usage, streamErr := s.llm.Stream(ctx, full, func(delta string) {
		answer.WriteString(delta)
		onDelta(delta)
	})

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if streamErr != nil {
		// A client cancel (the request context is done -- the browser
		// disconnected or the question was aborted) is not refunded: the
		// provider call carried the same ctx, so it may already have been
		// billed upstream even though nothing came back. Only a genuine
		// upstream failure before any text was sent gets its reservation
		// back.
		clientGone := ctx.Err() != nil
		if !clientGone {
			log.Printf("hack: assistant provider: %v", streamErr)
			if answer.Len() == 0 {
				if err := eventrepo.AssistantRefund(cleanupCtx, s.db, userID, day, month, reserveMicro); err != nil {
					log.Printf("hack: assistant refund: %v", err)
				}
			}
		}
		return AskResult{}, &AssistantRefusal{Code: "provider_error", Message: "The assistant couldn't answer. Try again."}
	}

	// If the provider gave no usage, keep the reservation as the cost
	// (delta 0 -- AssistantSettle is then a no-op). Settling to the
	// actual cost, uncapped, means one request's real cost can exceed its
	// worst-case reservation (the model ran longer than the MaxTokens
	// estimate priced for) and so push this month's ledger that one
	// request's excess past the budget; documented, not fixed, since only
	// one request can ever be in flight past the cap this way
	// (FEATURES.md "Organiser assistant").
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		actualMicro := int64(math.Round(float64(usage.PromptTokens)*s.cfg.PriceInputPerMTokUSD + float64(usage.CompletionTokens)*s.cfg.PriceOutputPerMTokUSD))
		if err := eventrepo.AssistantSettle(cleanupCtx, s.db, month, actualMicro-reserveMicro); err != nil {
			log.Printf("hack: assistant settle: %v", err)
		}
	}

	// The provider can "succeed" (a clean [DONE]/finish_reason) with only
	// whitespace, e.g. a reasoning model that spent every token thinking.
	// The call was billed, so the question and its cost stay counted (a
	// refund here would let anyone ask for an empty reply for free); the
	// organiser sees provider_error and can retry.
	if strings.TrimSpace(answer.String()) == "" {
		log.Printf("hack: assistant provider: %v", errAssistantEmptyAnswer)
		return AskResult{}, &AssistantRefusal{Code: "provider_error", Message: "The assistant couldn't answer. Try again."}
	}

	usedToday, err := eventrepo.AssistantUsedToday(cleanupCtx, s.db, userID, day)
	if err != nil {
		log.Printf("hack: assistant used-today: %v", err)
	}
	remaining := s.cfg.DailyQuestions - usedToday
	if remaining < 0 {
		remaining = 0
	}
	return AskResult{RemainingToday: remaining}, nil
}
