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

	"github.com/vsriram/simple-host/host/llm"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

const AssistantMaxTokens = 800

// Caps on how much of the event view and the docs text are embedded in the system prompt.
const (
	assistantEventViewCapBytes = 24 * 1024
	assistantDocsCapBytes      = 16 * 1024
)

// AssistantLLM is the subset of *llm.Client's API AssistantService needs; tests substitute a fake.
type AssistantLLM interface {
	Stream(ctx context.Context, messages []llm.Message, onDelta func(string)) (llm.Usage, error)
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// DocsTextFunc returns the pre-built, already-capped DOCS block (each page as "## [<title>](<url>)" plus its extracted text).
type DocsTextFunc func() string

// AssistantConfig holds the assistant's settings (ASSISTANT_* env vars), threaded through from cmd/server.
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

// AssistantService answers an organiser's questions about one event, built only from the shared navigation map, that event's own view and the Hack docs text.
type AssistantService struct {
	db       *sql.DB
	llm      AssistantLLM
	cfg      AssistantConfig
	docsText DocsTextFunc
	enabled  bool
}

func NewAssistant(database *sql.DB, llmClient AssistantLLM, cfg AssistantConfig, docsText DocsTextFunc) *AssistantService {
	return &AssistantService{
		db:       database,
		llm:      llmClient,
		cfg:      cfg,
		docsText: docsText,
		enabled:  strings.TrimSpace(cfg.BaseURL) != "" && strings.TrimSpace(cfg.Model) != "",
	}
}

func (s *AssistantService) Enabled() bool { return s.enabled }

type AssistantStatus struct {
	Enabled        bool
	DailyLimit     int
	UsedToday      int
	RemainingToday int
	BudgetReached  bool
	// Resets is the first day of next month (UTC, YYYY-MM-DD); only meaningful when BudgetReached.
	Resets string
}

// AssistantRefusal is every way Ask can refuse.
type AssistantRefusal struct {
	Code    string
	Message string
	// RemainingToday is set (always 0) for daily_limit; Resets (YYYY-MM-DD, UTC) is set for monthly_budget.
	RemainingToday int
	Resets         string
}

func (e *AssistantRefusal) Error() string { return e.Message }

// errAssistantEmptyAnswer is only logged.
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
	remaining := max(s.cfg.DailyQuestions-used, 0)
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

type AskResult struct {
	RemainingToday int
}

const (
	assistantMaxTurnRunes  = 2000
	assistantMaxTotalRunes = 12000
)

// validateAssistantMessages enforces the contract's request shape: 1..20 messages, the last one role "user".
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

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

// AskContext is everything about the asking organiser's event and screen that the prompt is built from.
type AskContext struct {
	Slug      string
	CanJudge  bool
	EventView json.RawMessage
	Settings  string
	Screen    string
	Tab       string
}

func (s *AssistantService) buildSystemPrompt(in AskContext, now time.Time) string {
	var b strings.Builder
	b.WriteString(assistantPromptRules)

	nav := rules.Navigation()
	if screen := assistantScreenLabel(nav, in.Screen, in.Tab); screen != "" {
		b.WriteString("\n\nCURRENT SCREEN: " + screen + "\n")
	}
	b.WriteString("\n\nPLACES:\n" + assistantPlaces(nav, in.Slug, in.CanJudge))
	b.WriteString("\nKNOWLEDGE:\n" + assistantKnowledge(in.Slug))

	settings := in.Settings
	if strings.TrimSpace(settings) == "" {
		settings = "(unavailable)\n"
	}
	b.WriteString("\nCURRENT SETTINGS:\n" + settings)
	b.WriteString("\nEVENT:\n" + capText(string(in.EventView), assistantEventViewCapBytes))

	docs := ""
	if s.docsText != nil {
		docs = s.docsText()
	}
	b.WriteString("\n\nDOCS:\n" + capText(docs, assistantDocsCapBytes))
	fmt.Fprintf(&b, "\n\nNow: %s\n", now.UTC().Format(time.RFC3339))
	return b.String()
}

func assistantPlaces(nav rules.NavigationMap, slug string, canJudge bool) string {
	var b strings.Builder
	seen := make(map[string]bool)
	for _, t := range nav.Targets {
		if t.Role != "organiser" && !(canJudge && t.Role == "judge") {
			continue
		}
		href := strings.ReplaceAll(t.HrefPattern, "{slug}", url.PathEscape(slug))
		key := t.Label + "\x00" + href
		if href == "" || strings.Contains(href, "{") || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&b, "- [%s](%s)\n", t.Label, href)
	}
	return b.String()
}

func capText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}

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

// assistantScreenLabel names the organiser screen (and tab) the question was asked from, from the navigation map; "" for anything it doesn't know.
func assistantScreenLabel(nav rules.NavigationMap, screen, tab string) string {
	screen, tab = strings.TrimSpace(screen), strings.TrimSpace(tab)
	label := organiserScreenLabel(nav, screen)
	switch {
	case label == "" || tab == "":
		return label
	case screen == "organiser-settings":
		for _, st := range nav.Resolution.SettingsTabs {
			if st.ID == tab {
				return label + " > " + st.Label
			}
		}
		return label
	case !plainTabID(tab):
		return label
	}
	return label + " (tab " + tab + ")"
}

// organiserScreenLabel finds an organiser screen's label: its screen target's label first, else its screen section; "" when unknown.
func organiserScreenLabel(nav rules.NavigationMap, screen string) string {
	if screen == "" {
		return ""
	}
	for _, t := range nav.Targets {
		if t.Role == "organiser" && t.Kind == "screen" && t.Screen == screen {
			if t.Label != "" {
				return t.Label
			}
			break
		}
	}
	for _, sc := range nav.Screens {
		if sc.Role == "organiser" && sc.ID == screen {
			return sc.Section
		}
	}
	return ""
}

// plainTabID reports whether a browser-sent tab id is short lowercase letters, digits, dashes and underscores, safe to echo into the prompt.
func plainTabID(tab string) bool {
	if len(tab) > 40 {
		return false
	}
	for _, r := range tab {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

const assistantPromptRules = `You help an organiser run this one event in Simple Hack. Answer questions about what any organiser setting, option or action means and does, where it is, how to do something, and what happens if they do it.
Answer in one to three short lines unless they ask for more detail, and always end with a link to the most relevant place.
Use only the facts below. KNOWLEDGE explains every organiser screen, setting, option and action; CURRENT SETTINGS and EVENT are this event's own values; CURRENT SCREEN is where the organiser is asking from. For a yes-or-no question, start your answer with "Yes" or "No".
If the facts don't answer the question, say "I'm not sure" and link the closest place or docs page. Never guess, and never fill a gap with what other apps usually do.
You cannot change anything in Simple Hack, so never say you changed or will change something; to act, link where the organiser does it themselves.
Links: copy a whole markdown link exactly as it appears in PLACES, KNOWLEDGE or DOCS. The href always starts with /e/ or /v1/skills/; never add a domain, a space or a second slash.
Do not use headings.`

// estimateTokens is the contract's worst-case reservation heuristic: roughly 3 bytes per token.
func estimateTokens(byteLen int) int {
	return byteLen / 3
}

const assistantTypicalPromptBytes = 64 * 1024

func (s *AssistantService) minReserveMicro() int64 {
	return s.costMicro(estimateTokens(assistantTypicalPromptBytes), AssistantMaxTokens)
}

func (s *AssistantService) costMicro(inputTokens, outputTokens int) int64 {
	return int64(math.Round(float64(inputTokens)*s.cfg.PriceInputPerMTokUSD + float64(outputTokens)*s.cfg.PriceOutputPerMTokUSD))
}

// providerRefusal is the one refusal for any failed or empty provider answer.
func providerRefusal() *AssistantRefusal {
	return &AssistantRefusal{Code: "provider_error", Message: "The assistant couldn't answer. Try again."}
}

// Ask validates the request, reserves the worst-case cost against the daily and monthly limits, streams the answer through onDelta, and settles.
func (s *AssistantService) Ask(ctx context.Context, userID string, in AskContext, messages []ChatMessage, onDelta func(string)) (AskResult, error) {
	if !s.enabled {
		return AskResult{}, &AssistantRefusal{Code: "assistant_off", Message: "The assistant is not set up on this server."}
	}
	if refusal := validateAssistantMessages(messages); refusal != nil {
		return AskResult{}, refusal
	}

	now := time.Now()
	day, month := assistantDay(now), assistantMonth(now)
	full, inputBytes := assistantConversation(s.buildSystemPrompt(in, now), messages)
	reserveMicro := s.costMicro(estimateTokens(inputBytes), AssistantMaxTokens)
	if err := eventrepo.AssistantReserve(ctx, s.db, userID, day, month, s.cfg.DailyQuestions, usdToMicro(s.cfg.MonthlyBudgetUSD), reserveMicro); err != nil {
		return AskResult{}, s.reserveRefusal(err, now)
	}

	var answer strings.Builder
	usage, streamErr := s.llm.Stream(ctx, full, func(delta string) {
		answer.WriteString(delta)
		onDelta(delta)
	})

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if streamErr != nil {
		// Log only provider failures, not a client cancel (request context done).
		if ctx.Err() == nil {
			log.Printf("hack: assistant provider: %v", streamErr)
			if answer.Len() == 0 {
				if err := eventrepo.AssistantRefund(cleanupCtx, s.db, userID, day, month, reserveMicro); err != nil {
					log.Printf("hack: assistant refund: %v", err)
				}
			}
		}
		return AskResult{}, providerRefusal()
	}

	s.settle(cleanupCtx, month, usage, reserveMicro)

	// The provider can "succeed" (a clean [DONE]/finish_reason) with only whitespace, e.g. a reasoning model that spent every token thinking.
	if strings.TrimSpace(answer.String()) == "" {
		log.Printf("hack: assistant provider: %v", errAssistantEmptyAnswer)
		return AskResult{}, providerRefusal()
	}

	usedToday, err := eventrepo.AssistantUsedToday(cleanupCtx, s.db, userID, day)
	if err != nil {
		log.Printf("hack: assistant used-today: %v", err)
	}
	return AskResult{RemainingToday: max(s.cfg.DailyQuestions-usedToday, 0)}, nil
}

func assistantConversation(systemPrompt string, messages []ChatMessage) ([]llm.Message, int) {
	full := []llm.Message{{Role: "system", Content: systemPrompt}}
	inputBytes := len(systemPrompt)
	for _, m := range messages {
		full = append(full, llm.Message{Role: m.Role, Content: m.Content})
		inputBytes += len(m.Content)
	}
	return full, inputBytes
}

// reserveRefusal turns a failed reservation into the organiser-facing refusal for the daily or monthly limit, passing any other error through.
func (s *AssistantService) reserveRefusal(err error, now time.Time) error {
	switch {
	case errors.Is(err, eventrepo.ErrAssistantDailyLimit):
		return &AssistantRefusal{
			Code:    "daily_limit",
			Message: fmt.Sprintf("You've asked %d questions today. The limit resets at midnight UTC.", s.cfg.DailyQuestions),
		}
	case errors.Is(err, eventrepo.ErrAssistantMonthlyBudget):
		next := assistantNextMonth(now)
		return &AssistantRefusal{
			Code:    "monthly_budget",
			Message: "The assistant has used this month's budget. It's back on " + next.Format("2 January") + ".",
			Resets:  next.Format("2006-01-02"),
		}
	}
	return err
}

func (s *AssistantService) settle(ctx context.Context, month time.Time, usage llm.Usage, reserveMicro int64) {
	if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 {
		return
	}
	if err := eventrepo.AssistantSettle(ctx, s.db, month, s.costMicro(usage.PromptTokens, usage.CompletionTokens)-reserveMicro); err != nil {
		log.Printf("hack: assistant settle: %v", err)
	}
}
