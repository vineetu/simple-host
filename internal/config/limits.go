package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Limits holds every operational time and limit an operator can change per
// install (docs/configuration.md lists them). Each one is read from its env
// var once, in Load, and checked against a range; a bad value stops the server
// at startup with a message naming the variable. Unset means today's value, so
// an install that sets none of them behaves exactly as before.
//
// The rest of the program reads the values through Active(), which
// handler.ApplyLimits sets once before serving (and hands the leaf packages
// their share). Code that states one of these values to a person (an
// email, a page, an MCP tool description) formats it from here, so changing a
// knob changes the words too.
type Limits struct {
	// Accounts and keys.
	SigninCodeTTL      time.Duration // SIGNIN_CODE_TTL_MINUTES
	MaxKeysPerAccount  int           // MAX_KEYS_PER_ACCOUNT
	KeyIdleExpiry      time.Duration // KEY_IDLE_EXPIRY_DAYS (0 = never)
	HandleRenameEvery  time.Duration // HANDLE_RENAME_EVERY_DAYS
	EmailChangeUndoTTL time.Duration // EMAIL_CHANGE_UNDO_DAYS

	// Sites.
	MaxSitesPerAccount int           // MAX_SITES_PER_ACCOUNT
	MaxFilesPerSite    int           // MAX_FILES_PER_SITE
	PreviewLinkTTL     time.Duration // PREVIEW_LINK_TTL_MINUTES
	ExportLinkTTL      time.Duration // EXPORT_LINK_TTL_MINUTES

	// Visitors signed in on a site's own address.
	VisitorSessionTTL  time.Duration // VISITOR_SESSION_DAYS
	VisitorSessionIdle time.Duration // VISITOR_SESSION_IDLE_DAYS

	// Connector (AI apps over /mcp).
	OAuthAccessTTL       time.Duration // OAUTH_ACCESS_TTL_MINUTES
	OAuthRefreshTTL      time.Duration // OAUTH_REFRESH_TTL_DAYS
	OAuthUnusedClientAge time.Duration // OAUTH_UNUSED_CLIENT_DAYS

	// Domains and certificates.
	DomainUnprovenTTL    time.Duration // DOMAIN_UNPROVEN_HOURS
	DomainUnprovenMaxAge time.Duration // DOMAIN_UNPROVEN_MAX_DAYS
	DomainLapseWarnAfter time.Duration // DOMAIN_LAPSE_WARN_HOURS
	DomainLapseAfter     time.Duration // DOMAIN_LAPSE_HOURS
	DomainCheckInterval  time.Duration // DOMAIN_CHECK_INTERVAL_MINUTES
	DomainCertsDaily     int           // DOMAIN_CERTS_PER_ACCOUNT_DAILY
	EventTTL             time.Duration // EVENT_TTL_DAYS
	EventMaxClaims       int           // EVENT_MAX_CLAIMS

	// Cleanup and retention.
	DeletedRetention    time.Duration // DELETED_RETENTION_DAYS
	IdleAfter           time.Duration // IDLE_AFTER_DAYS
	IdleGrace           time.Duration // IDLE_GRACE_DAYS
	AnalyticsRetention  int           // ANALYTICS_RETENTION_DAYS (days)
	AnalyticsPagesDay   int           // ANALYTICS_PAGES_PER_SITE_DAY
	AnalyticsRefsDay    int           // ANALYTICS_REFERRERS_PER_SITE_DAY
	APIMetricsRetention int           // API_METRICS_RETENTION_DAYS (days)
	IdleReplyTo         string        // IDLE_REPLY_TO

	// AI create.
	AIMaxJobsPerUser int           // AI_MAX_JOBS_PER_USER
	AIMaxJobs        int           // AI_MAX_JOBS
	AIJobTimeout     time.Duration // AI_JOB_TIMEOUT_MINUTES

	// Rate limits.
	RateSigninIP        Rate // RATE_LIMIT_SIGNIN_IP
	RateSigninEmail     Rate // RATE_LIMIT_SIGNIN_EMAIL
	RateVisitorOAuth    Rate // RATE_LIMIT_VISITOR_OAUTH
	RateVisitorAuth     Rate // RATE_LIMIT_VISITOR_AUTH
	RateVisitor         Rate // RATE_LIMIT_VISITOR
	RateUpload          Rate // RATE_LIMIT_UPLOAD
	RateState           Rate // RATE_LIMIT_STATE
	RateSiteOps         Rate // RATE_LIMIT_SITE_OPS
	RateExport          Rate // RATE_LIMIT_EXPORT
	RateAnalytics       Rate // RATE_LIMIT_ANALYTICS
	RateTLSAsk          Rate // RATE_LIMIT_TLS_ASK
	RateDomainCheck     Rate // RATE_LIMIT_DOMAIN_CHECK
	RateDomainCheckUser Rate // RATE_LIMIT_DOMAIN_CHECK_USER
	RateHandleCheck     Rate // RATE_LIMIT_HANDLE_CHECK
	RateOAuthRegister   Rate // RATE_LIMIT_OAUTH_REGISTER
	RateOAuthAuthorize  Rate // RATE_LIMIT_OAUTH_AUTHORIZE
	RateOAuthToken      Rate // RATE_LIMIT_OAUTH_TOKEN
	RateAIIP            Rate // RATE_LIMIT_AI_IP
	RateAIUser          Rate // RATE_LIMIT_AI_USER
	RateTranscribe      Rate // RATE_LIMIT_TRANSCRIBE

	// Saved data (SAVED_DATA_*): history, undo, the watch and the limits.
	SavedData SavedData

	// "Ask about this page" (ASK_*).
	Ask Ask
}

// Ask is the "Ask about this page" box (POST /v1/ask). It runs only when the
// LLM_* backend is configured and Enabled is on. Burst questions per address,
// then one every EverySeconds; DailyMax questions per UTC day across everyone
// (counted in Postgres), because the subscription behind the backend is
// shared; at most MaxInFlight answered at once.
type Ask struct {
	Enabled      bool // ASK_ENABLED: on/off (on)
	Burst        int  // ASK_BURST (5)
	EverySeconds int  // ASK_EVERY_SECONDS (20)
	DailyMax     int  // ASK_DAILY_MAX (500)
	MaxInFlight  int  // ASK_MAX_IN_FLIGHT (4)
	// The model the box asks, separate from LLM_MODEL (AI create keeps its
	// own): a fast model with reasoning off answers in seconds.
	Model           string // ASK_MODEL (grok-4.7)
	ReasoningEffort string // ASK_REASONING_EFFORT: none, low, medium or high (none)
	MaxTokens       int    // ASK_MAX_TOKENS (300)
	// The setup helper's optional "Check my choices" (POST /v1/setup/check)
	// uses the same backend, model, per-address limits and in-flight cap,
	// with its own count per UTC day (table setup_check_daily).
	SetupCheckDailyMax int // SETUP_CHECK_DAILY_MAX (200); 0 turns the check off
	// Its own in-flight cap, so Ask always keeps its slots, and a count per
	// network (/24 or /48) per UTC day, so one network cannot use up the day.
	SetupCheckMaxInFlight     int // SETUP_CHECK_MAX_IN_FLIGHT (1)
	SetupCheckPerNetworkDaily int // SETUP_CHECK_PER_NETWORK_DAILY (20)
	// The setup helper's assistant (POST /v1/setup/assist): the same backend,
	// model and per-address limits, with its own count per UTC day (table
	// setup_assist_daily), its own in-flight cap and count per network.
	SetupAssistDailyMax        int // SETUP_ASSIST_DAILY_MAX (300); 0 turns the assistant off
	SetupAssistMaxInFlight     int // SETUP_ASSIST_MAX_IN_FLIGHT (1)
	SetupAssistPerNetworkDaily int // SETUP_ASSIST_PER_NETWORK_DAILY (40)
}

// SavedData is every number behind saved-data history, undo, the watch and
// the limits (saved-data redesign step 1, 2026-09-27). Each is a knob in
// Knobs(); the defaults are the approved plan's values.
type SavedData struct {
	UndoDays         int // SAVED_DATA_UNDO_DAYS: history and deleted items are kept this long (30)
	HistoryMaxMB     int // SAVED_DATA_HISTORY_MAX_MB: per-site history cap before thinning (20)
	SiteMaxMB        int // SAVED_DATA_SITE_MAX_MB: per-site live saved data (page data + list items; not history or Recently deleted) (50)
	SweepMinutes     int // SAVED_DATA_SWEEP_MINUTES: how often expired history is removed (15)
	WatchDays        int // SAVED_DATA_WATCH_DAYS: the watch window before tightening (7)
	WatchIncMax      int // SAVED_DATA_WATCH_INC_MAX: a visitor inc larger than this is counted as large (10)
	WatchItemKB      int // SAVED_DATA_WATCH_ITEM_KB: a list item larger than this is counted as large (16)
	IdempotencyHours int // SAVED_DATA_IDEMPOTENCY_HOURS: how long a write's first answer is replayed (24)
	ReadPerSec       int // SAVED_DATA_READ_PER_SEC: saved-data reads per second per site and address, or per key (30)
	ReadBurst        int // SAVED_DATA_READ_BURST: burst above that rate (60)
	// SAVED_DATA_APPEND_PER_MIN / _BURST: list items one address may add per
	// minute when not writing with the owner's key (30 / 30).
	AppendPerMin int
	AppendBurst  int
	// SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE: remembered Idempotency-Keys kept
	// per site; the sweep drops the oldest past it (10000).
	IdempotencyMaxPerSite int
	// SAVED_DATA_SNAPSHOT_EVERY: a PATCH's history keeps only what it
	// changed, with a full copy of the document at least every this many
	// changes and on the first change of each day (50).
	SnapshotEvery int
	// SAVED_DATA_WATCH_KEEP_DAYS: watch counts older than this are removed (90).
	WatchKeepDays int

	// Step 2, kinds (Page info and Submissions).
	// SAVED_DATA_CONTENT_MAX_KB: one Page info document (1024).
	ContentMaxKB int
	// SAVED_DATA_CONTENT_NAMES_MAX: Page info names per site (20).
	ContentNamesMax int
	// SAVED_DATA_ENTRY_MAX_KB: one new Submissions entry; older items up to
	// the 64 KB list limit are kept (16).
	EntryMaxKB int
	// SAVED_DATA_ENTRIES_MAX: live entries in one Submissions name (10000).
	EntriesMax int
	// SAVED_DATA_WITHDRAW_UNDO_MINUTES: a visitor can bring back an entry
	// they withdrew for this long (10).
	WithdrawUndoMinutes int
	// SAVED_DATA_NOTIFY_EACH_MINUTES: "email me: each" sends at most one
	// email per name this often, listing what arrived (10).
	NotifyEachMinutes int
	// SAVED_DATA_NOTIFY_DAILY_HOURS: "email me: daily" sends at most one
	// digest per name this often (24).
	NotifyDailyHours int
	// SAVED_DATA_SAVERS_MAX: emails and domains in one site's who-may-save
	// and block lists together (500).
	SaversMax int
	// SAVED_DATA_ENTRIES_NAMES_MAX: Submissions names per site (50).
	EntriesNamesMax int

	// Steps 3 and 4: Personal (mine) and Shared board (board).
	// SAVED_DATA_PERSONAL_MAX_KB: one person's record in one Personal name (64).
	PersonalMaxKB int
	// SAVED_DATA_PERSONAL_NAMES_MAX: Personal names per site (20).
	PersonalNamesMax int
	// SAVED_DATA_BOARD_ITEM_MAX_KB: one Shared board item (16).
	BoardItemMaxKB int
	// SAVED_DATA_BOARD_MAX: live items in one Shared board (2000).
	BoardMax int
	// SAVED_DATA_BOARD_NAMES_MAX: Shared board names per site (20).
	BoardNamesMax int
	// SAVED_DATA_PERSONAL_PEOPLE_MAX: people with a record in one Personal
	// name (1000).
	PersonalPeopleMax int
	// SAVED_DATA_BOARD_WRITES_PER_MIN: board adds, changes and deletes per
	// signed-in person per minute, on top of the per-address rate (30).
	BoardWritesPerMin int
	// SAVED_DATA_DEFAULT_KIND: what a name nobody declared is on a site made
	// after the kinds. "shared" (default): Shared, anyone reads it and
	// signed-in visitors save to it, as before the kinds. "declare_first":
	// it takes no saves until the owner declares it. Sites that existed
	// before the kinds are Shared either way.
	DefaultKind string
}

// The SAVED_DATA_DEFAULT_KIND values.
const (
	DefaultKindShared       = "shared"
	DefaultKindDeclareFirst = "declare_first"
)

// DefaultSavedData is the approved plan's values.
func DefaultSavedData() SavedData {
	return SavedData{UndoDays: 30, HistoryMaxMB: 20, SiteMaxMB: 50, SweepMinutes: 15, WatchDays: 7,
		WatchIncMax: 10, WatchItemKB: 16, IdempotencyHours: 24, ReadPerSec: 30, ReadBurst: 60,
		AppendPerMin: 30, AppendBurst: 30, IdempotencyMaxPerSite: 10000, SnapshotEvery: 50, WatchKeepDays: 90,
		ContentMaxKB: 1024, ContentNamesMax: 20, EntryMaxKB: 16, EntriesMax: 10000, WithdrawUndoMinutes: 10,
		NotifyEachMinutes: 10, NotifyDailyHours: 24, SaversMax: 500, EntriesNamesMax: 50,
		PersonalMaxKB: 64, PersonalNamesMax: 20, BoardItemMaxKB: 16, BoardMax: 2000, BoardNamesMax: 20,
		PersonalPeopleMax: 1000, BoardWritesPerMin: 30,
		DefaultKind: DefaultKindShared}
}

// Rate is a token bucket: Burst requests at once, then one more every Every.
// Written in env as "<burst>,<every>", e.g. RATE_LIMIT_SIGNIN_IP=20,5s.
type Rate struct {
	Burst int
	Every time.Duration
}

// PerSecond is the refill rate the limiters take.
func (r Rate) PerSecond() float64 { return 1 / r.Every.Seconds() }

func (r Rate) String() string { return fmt.Sprintf("%d,%s", r.Burst, shortDuration(r.Every)) }

// shortDuration prints 5s, 6m, 1h rather than 5s, 6m0s, 1h0m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

const day = 24 * time.Hour

// DefaultLimits is today's behaviour: the values the code had built in before
// they became settings.
func DefaultLimits() Limits {
	return Limits{
		SigninCodeTTL:      15 * time.Minute,
		MaxKeysPerAccount:  50,
		KeyIdleExpiry:      180 * day,
		HandleRenameEvery:  30 * day,
		EmailChangeUndoTTL: 7 * day,

		MaxSitesPerAccount: 100,
		MaxFilesPerSite:    50_000,
		PreviewLinkTTL:     time.Hour,
		ExportLinkTTL:      10 * time.Minute,

		VisitorSessionTTL:  30 * day,
		VisitorSessionIdle: 14 * day,

		OAuthAccessTTL:       time.Hour,
		OAuthRefreshTTL:      90 * day,
		OAuthUnusedClientAge: 30 * day,

		DomainUnprovenTTL:    24 * time.Hour,
		DomainUnprovenMaxAge: 7 * day,
		DomainLapseWarnAfter: 24 * time.Hour,
		DomainLapseAfter:     72 * time.Hour,
		DomainCheckInterval:  2 * time.Minute,
		DomainCertsDaily:     5,
		EventTTL:             21 * day,
		EventMaxClaims:       5,

		DeletedRetention:    7 * day,
		IdleAfter:           90 * day,
		IdleGrace:           30 * day,
		AnalyticsRetention:  400,
		AnalyticsPagesDay:   200,
		AnalyticsRefsDay:    100,
		APIMetricsRetention: 30,
		IdleReplyTo:         "support@simple-host.app",

		AIMaxJobsPerUser: 3,
		AIMaxJobs:        64,
		AIJobTimeout:     8 * time.Minute,

		RateSigninIP:        Rate{20, 5 * time.Second},
		RateSigninEmail:     Rate{5, 50 * time.Second},
		RateVisitorOAuth:    Rate{20, 5 * time.Second},
		RateVisitorAuth:     Rate{20, 5 * time.Second},
		RateVisitor:         Rate{20, 5 * time.Second},
		RateUpload:          Rate{30, 10 * time.Second},
		RateState:           Rate{60, time.Second},
		RateSiteOps:         Rate{30, 2 * time.Second},
		RateExport:          Rate{10, 10 * time.Second},
		RateAnalytics:       Rate{30, 2 * time.Second},
		RateTLSAsk:          Rate{60, 100 * time.Millisecond},
		RateDomainCheck:     Rate{10, 10 * time.Second},
		RateDomainCheckUser: Rate{3, 30 * time.Second},
		RateHandleCheck:     Rate{30, 2 * time.Second},
		RateOAuthRegister:   Rate{10, 6 * time.Minute},
		RateOAuthAuthorize:  Rate{30, 2 * time.Second},
		RateOAuthToken:      Rate{30, 2 * time.Second},
		RateAIIP:            Rate{20, 12 * time.Second},
		RateAIUser:          Rate{30, 10 * time.Second},
		RateTranscribe:      Rate{60, 3 * time.Second},

		SavedData: DefaultSavedData(),

		Ask: Ask{Enabled: true, Burst: 5, EverySeconds: 20, DailyMax: 500, MaxInFlight: 4,
			Model: "grok-4.7", ReasoningEffort: "none", MaxTokens: 300, SetupCheckDailyMax: 200,
			SetupCheckMaxInFlight: 1, SetupCheckPerNetworkDaily: 20,
			SetupAssistDailyMax: 300, SetupAssistMaxInFlight: 1, SetupAssistPerNetworkDaily: 40},
	}
}

// Knob describes one setting for the docs table and the startup log.
type Knob struct {
	Env, Unit string
	Min, Max  int64
	// Sensitive marks a rate limit that guards sign-in or the connector's
	// OAuth: it may be made stricter freely but at most secLoosen times
	// looser than its default.
	Sensitive bool
	// Value reads the current setting out of a Limits in the knob's unit, as
	// the env var would spell it.
	Value func(*Limits) string
	set   func(*Limits, string) error
}

func intKnob(env, unit string, min, max int64, p func(*Limits) *int) Knob {
	return Knob{Env: env, Unit: unit, Min: min, Max: max,
		Value: func(l *Limits) string { return strconv.Itoa(*p(l)) },
		set: func(l *Limits, v string) error {
			n, err := parseRange(env, v, min, max)
			*p(l) = int(n)
			return err
		}}
}

func durKnob(env, unit string, per time.Duration, min, max int64, p func(*Limits) *time.Duration) Knob {
	return Knob{Env: env, Unit: unit, Min: min, Max: max,
		Value: func(l *Limits) string { return strconv.FormatInt(int64(*p(l)/per), 10) },
		set: func(l *Limits, v string) error {
			n, err := parseRange(env, v, min, max)
			*p(l) = time.Duration(n) * per
			return err
		}}
}

// boolKnob is an on/off setting: on, true, 1 or yes; off, false, 0 or no
// (any case). Anything else is an error.
func boolKnob(env string, p func(*Limits) *bool) Knob {
	return Knob{Env: env, Unit: "on/off",
		Value: func(l *Limits) string {
			if *p(l) {
				return "on"
			}
			return "off"
		},
		set: func(l *Limits, v string) error {
			switch strings.ToLower(v) {
			case "on", "true", "1", "yes":
				*p(l) = true
			case "off", "false", "0", "no":
				*p(l) = false
			default:
				return fmt.Errorf("%s=%q: want on or off (also true/false, 1/0, yes/no)", env, v)
			}
			return nil
		}}
}

func rateKnob(env string, p func(*Limits) *Rate) Knob {
	return Knob{Env: env, Unit: "burst,every",
		Value: func(l *Limits) string { return p(l).String() },
		set: func(l *Limits, v string) error {
			r, err := parseRate(env, v)
			*p(l) = r
			return err
		}}
}

// secLoosen is how far a security-sensitive rate limit may be loosened: a
// burst at most this many times the default, a refill interval at least the
// default divided by it. warnLoosen is the point past which any other rate
// limit is named in a startup warning.
const (
	secLoosen  = 4
	warnLoosen = 10
)

// secRateKnob is a rate limit guarding sign-in, visitor sign-in or the
// connector's OAuth. Loosening it past secLoosen stops startup.
func secRateKnob(env string, p func(*Limits) *Rate) Knob {
	k := rateKnob(env, p)
	k.Sensitive = true
	dl := DefaultLimits()
	def := *p(&dl)
	parse := k.set
	k.set = func(l *Limits, v string) error {
		if err := parse(l, v); err != nil {
			return err
		}
		if r := *p(l); r.Burst > def.Burst*secLoosen || r.Every < def.Every/secLoosen {
			return fmt.Errorf("%s=%q is too loose for a sign-in limit: at most %d at once and an <every> of at least %s (%d times the default %s). It can be made stricter freely",
				env, v, def.Burst*secLoosen, shortDuration(def.Every/secLoosen), secLoosen, def)
		}
		return nil
	}
	return k
}

// askModelName is what ASK_MODEL may hold.
var askModelName = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,100}$`)

// Knobs lists every setting, in the order docs/configuration.md gives them.
func Knobs() []Knob {
	m, h, d := time.Minute, time.Hour, day
	return []Knob{
		durKnob("SIGNIN_CODE_TTL_MINUTES", "minutes", m, 5, 60, func(l *Limits) *time.Duration { return &l.SigninCodeTTL }),
		intKnob("MAX_KEYS_PER_ACCOUNT", "keys", 1, 1000, func(l *Limits) *int { return &l.MaxKeysPerAccount }),
		// 0 turns idle expiry off: keys then work until revoked.
		durKnob("KEY_IDLE_EXPIRY_DAYS", "days", d, 0, 3650, func(l *Limits) *time.Duration { return &l.KeyIdleExpiry }),
		durKnob("HANDLE_RENAME_EVERY_DAYS", "days", d, 7, 365, func(l *Limits) *time.Duration { return &l.HandleRenameEvery }),
		durKnob("EMAIL_CHANGE_UNDO_DAYS", "days", d, 1, 90, func(l *Limits) *time.Duration { return &l.EmailChangeUndoTTL }),

		intKnob("MAX_SITES_PER_ACCOUNT", "sites", 1, 100_000, func(l *Limits) *int { return &l.MaxSitesPerAccount }),
		intKnob("MAX_FILES_PER_SITE", "files", 100, 50_000, func(l *Limits) *int { return &l.MaxFilesPerSite }),
		durKnob("PREVIEW_LINK_TTL_MINUTES", "minutes", m, 5, 7*24*60, func(l *Limits) *time.Duration { return &l.PreviewLinkTTL }),
		durKnob("EXPORT_LINK_TTL_MINUTES", "minutes", m, 1, 60, func(l *Limits) *time.Duration { return &l.ExportLinkTTL }),

		durKnob("VISITOR_SESSION_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.VisitorSessionTTL }),
		durKnob("VISITOR_SESSION_IDLE_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.VisitorSessionIdle }),

		durKnob("OAUTH_ACCESS_TTL_MINUTES", "minutes", m, 5, 24*60, func(l *Limits) *time.Duration { return &l.OAuthAccessTTL }),
		durKnob("OAUTH_REFRESH_TTL_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.OAuthRefreshTTL }),
		durKnob("OAUTH_UNUSED_CLIENT_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.OAuthUnusedClientAge }),

		durKnob("DOMAIN_UNPROVEN_HOURS", "hours", h, 1, 30*24, func(l *Limits) *time.Duration { return &l.DomainUnprovenTTL }),
		durKnob("DOMAIN_UNPROVEN_MAX_DAYS", "days", d, 1, 90, func(l *Limits) *time.Duration { return &l.DomainUnprovenMaxAge }),
		durKnob("DOMAIN_LAPSE_WARN_HOURS", "hours", h, 1, 30*24, func(l *Limits) *time.Duration { return &l.DomainLapseWarnAfter }),
		durKnob("DOMAIN_LAPSE_HOURS", "hours", h, 2, 90*24, func(l *Limits) *time.Duration { return &l.DomainLapseAfter }),
		durKnob("DOMAIN_CHECK_INTERVAL_MINUTES", "minutes", m, 1, 60, func(l *Limits) *time.Duration { return &l.DomainCheckInterval }),
		intKnob("DOMAIN_CERTS_PER_ACCOUNT_DAILY", "certificates", 1, 1000, func(l *Limits) *int { return &l.DomainCertsDaily }),
		durKnob("EVENT_TTL_DAYS", "days", d, 1, 60, func(l *Limits) *time.Duration { return &l.EventTTL }),
		intKnob("EVENT_MAX_CLAIMS", "names", 1, 100, func(l *Limits) *int { return &l.EventMaxClaims }),

		durKnob("DELETED_RETENTION_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.DeletedRetention }),
		durKnob("IDLE_AFTER_DAYS", "days", d, 7, 3650, func(l *Limits) *time.Duration { return &l.IdleAfter }),
		durKnob("IDLE_GRACE_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.IdleGrace }),
		intKnob("ANALYTICS_RETENTION_DAYS", "days", 1, 3650, func(l *Limits) *int { return &l.AnalyticsRetention }),
		// Distinct pages and referring domains kept per site per day; the
		// rest are counted together as "(other)".
		intKnob("ANALYTICS_PAGES_PER_SITE_DAY", "pages", 10, 10_000, func(l *Limits) *int { return &l.AnalyticsPagesDay }),
		intKnob("ANALYTICS_REFERRERS_PER_SITE_DAY", "domains", 10, 10_000, func(l *Limits) *int { return &l.AnalyticsRefsDay }),
		intKnob("API_METRICS_RETENTION_DAYS", "days", 1, 3650, func(l *Limits) *int { return &l.APIMetricsRetention }),
		{Env: "IDLE_REPLY_TO", Unit: "email address",
			Value: func(l *Limits) string { return l.IdleReplyTo },
			set: func(l *Limits, v string) error {
				if strings.ContainsAny(v, " \t\r\n<>,") || strings.Count(v, "@") != 1 || strings.HasPrefix(v, "@") || strings.HasSuffix(v, "@") {
					return fmt.Errorf("IDLE_REPLY_TO=%q is not a single email address (like support@example.com)", v)
				}
				l.IdleReplyTo = v
				return nil
			}},

		intKnob("AI_MAX_JOBS_PER_USER", "builds", 1, 100, func(l *Limits) *int { return &l.AIMaxJobsPerUser }),
		intKnob("AI_MAX_JOBS", "builds", 1, 1000, func(l *Limits) *int { return &l.AIMaxJobs }),
		// At most 8: the builder page polls for 9 minutes, so a longer run
		// would finish after the page has given up on it.
		durKnob("AI_JOB_TIMEOUT_MINUTES", "minutes", m, 1, 8, func(l *Limits) *time.Duration { return &l.AIJobTimeout }),

		secRateKnob("RATE_LIMIT_SIGNIN_IP", func(l *Limits) *Rate { return &l.RateSigninIP }),
		secRateKnob("RATE_LIMIT_SIGNIN_EMAIL", func(l *Limits) *Rate { return &l.RateSigninEmail }),
		secRateKnob("RATE_LIMIT_VISITOR_OAUTH", func(l *Limits) *Rate { return &l.RateVisitorOAuth }),
		secRateKnob("RATE_LIMIT_VISITOR_AUTH", func(l *Limits) *Rate { return &l.RateVisitorAuth }),
		secRateKnob("RATE_LIMIT_VISITOR", func(l *Limits) *Rate { return &l.RateVisitor }),
		rateKnob("RATE_LIMIT_UPLOAD", func(l *Limits) *Rate { return &l.RateUpload }),
		rateKnob("RATE_LIMIT_STATE", func(l *Limits) *Rate { return &l.RateState }),
		rateKnob("RATE_LIMIT_SITE_OPS", func(l *Limits) *Rate { return &l.RateSiteOps }),
		rateKnob("RATE_LIMIT_EXPORT", func(l *Limits) *Rate { return &l.RateExport }),
		rateKnob("RATE_LIMIT_ANALYTICS", func(l *Limits) *Rate { return &l.RateAnalytics }),
		rateKnob("RATE_LIMIT_TLS_ASK", func(l *Limits) *Rate { return &l.RateTLSAsk }),
		rateKnob("RATE_LIMIT_DOMAIN_CHECK", func(l *Limits) *Rate { return &l.RateDomainCheck }),
		rateKnob("RATE_LIMIT_DOMAIN_CHECK_USER", func(l *Limits) *Rate { return &l.RateDomainCheckUser }),
		rateKnob("RATE_LIMIT_HANDLE_CHECK", func(l *Limits) *Rate { return &l.RateHandleCheck }),
		secRateKnob("RATE_LIMIT_OAUTH_REGISTER", func(l *Limits) *Rate { return &l.RateOAuthRegister }),
		secRateKnob("RATE_LIMIT_OAUTH_AUTHORIZE", func(l *Limits) *Rate { return &l.RateOAuthAuthorize }),
		secRateKnob("RATE_LIMIT_OAUTH_TOKEN", func(l *Limits) *Rate { return &l.RateOAuthToken }),
		rateKnob("RATE_LIMIT_AI_IP", func(l *Limits) *Rate { return &l.RateAIIP }),
		rateKnob("RATE_LIMIT_AI_USER", func(l *Limits) *Rate { return &l.RateAIUser }),
		rateKnob("RATE_LIMIT_TRANSCRIBE", func(l *Limits) *Rate { return &l.RateTranscribe }),

		intKnob("SAVED_DATA_UNDO_DAYS", "days", 1, 365, func(l *Limits) *int { return &l.SavedData.UndoDays }),
		intKnob("SAVED_DATA_HISTORY_MAX_MB", "MB", 1, 10_240, func(l *Limits) *int { return &l.SavedData.HistoryMaxMB }),
		intKnob("SAVED_DATA_SITE_MAX_MB", "MB", 1, 10_240, func(l *Limits) *int { return &l.SavedData.SiteMaxMB }),
		intKnob("SAVED_DATA_SNAPSHOT_EVERY", "changes", 1, 10_000, func(l *Limits) *int { return &l.SavedData.SnapshotEvery }),
		intKnob("SAVED_DATA_SWEEP_MINUTES", "minutes", 1, 1440, func(l *Limits) *int { return &l.SavedData.SweepMinutes }),
		intKnob("SAVED_DATA_WATCH_DAYS", "days", 1, 365, func(l *Limits) *int { return &l.SavedData.WatchDays }),
		intKnob("SAVED_DATA_WATCH_INC_MAX", "count", 1, 1_000_000_000, func(l *Limits) *int { return &l.SavedData.WatchIncMax }),
		intKnob("SAVED_DATA_WATCH_ITEM_KB", "KB", 1, 64, func(l *Limits) *int { return &l.SavedData.WatchItemKB }),
		intKnob("SAVED_DATA_WATCH_KEEP_DAYS", "days", 1, 3650, func(l *Limits) *int { return &l.SavedData.WatchKeepDays }),
		intKnob("SAVED_DATA_IDEMPOTENCY_HOURS", "hours", 1, 720, func(l *Limits) *int { return &l.SavedData.IdempotencyHours }),
		intKnob("SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE", "keys", 100, 1_000_000, func(l *Limits) *int { return &l.SavedData.IdempotencyMaxPerSite }),
		intKnob("SAVED_DATA_READ_PER_SEC", "reads", 1, 10_000, func(l *Limits) *int { return &l.SavedData.ReadPerSec }),
		intKnob("SAVED_DATA_READ_BURST", "reads", 1, 100_000, func(l *Limits) *int { return &l.SavedData.ReadBurst }),
		intKnob("SAVED_DATA_APPEND_PER_MIN", "items", 1, 10_000, func(l *Limits) *int { return &l.SavedData.AppendPerMin }),
		intKnob("SAVED_DATA_APPEND_BURST", "items", 1, 100_000, func(l *Limits) *int { return &l.SavedData.AppendBurst }),
		intKnob("SAVED_DATA_CONTENT_MAX_KB", "KB", 1, 10_240, func(l *Limits) *int { return &l.SavedData.ContentMaxKB }),
		intKnob("SAVED_DATA_CONTENT_NAMES_MAX", "names", 1, 1_000, func(l *Limits) *int { return &l.SavedData.ContentNamesMax }),
		intKnob("SAVED_DATA_ENTRY_MAX_KB", "KB", 1, 64, func(l *Limits) *int { return &l.SavedData.EntryMaxKB }),
		intKnob("SAVED_DATA_ENTRIES_MAX", "entries", 1, 1_000_000, func(l *Limits) *int { return &l.SavedData.EntriesMax }),
		intKnob("SAVED_DATA_WITHDRAW_UNDO_MINUTES", "minutes", 1, 1440, func(l *Limits) *int { return &l.SavedData.WithdrawUndoMinutes }),
		intKnob("SAVED_DATA_NOTIFY_EACH_MINUTES", "minutes", 1, 1440, func(l *Limits) *int { return &l.SavedData.NotifyEachMinutes }),
		intKnob("SAVED_DATA_NOTIFY_DAILY_HOURS", "hours", 1, 720, func(l *Limits) *int { return &l.SavedData.NotifyDailyHours }),
		intKnob("SAVED_DATA_SAVERS_MAX", "entries", 1, 100_000, func(l *Limits) *int { return &l.SavedData.SaversMax }),
		intKnob("SAVED_DATA_ENTRIES_NAMES_MAX", "names", 1, 1_000, func(l *Limits) *int { return &l.SavedData.EntriesNamesMax }),
		intKnob("SAVED_DATA_PERSONAL_MAX_KB", "KB", 1, 1024, func(l *Limits) *int { return &l.SavedData.PersonalMaxKB }),
		intKnob("SAVED_DATA_PERSONAL_NAMES_MAX", "names", 1, 1_000, func(l *Limits) *int { return &l.SavedData.PersonalNamesMax }),
		intKnob("SAVED_DATA_BOARD_ITEM_MAX_KB", "KB", 1, 64, func(l *Limits) *int { return &l.SavedData.BoardItemMaxKB }),
		intKnob("SAVED_DATA_BOARD_MAX", "items", 1, 1_000_000, func(l *Limits) *int { return &l.SavedData.BoardMax }),
		intKnob("SAVED_DATA_BOARD_NAMES_MAX", "names", 1, 1_000, func(l *Limits) *int { return &l.SavedData.BoardNamesMax }),
		intKnob("SAVED_DATA_PERSONAL_PEOPLE_MAX", "people", 1, 1_000_000, func(l *Limits) *int { return &l.SavedData.PersonalPeopleMax }),
		intKnob("SAVED_DATA_BOARD_WRITES_PER_MIN", "writes", 1, 10_000, func(l *Limits) *int { return &l.SavedData.BoardWritesPerMin }),
		{Env: "SAVED_DATA_DEFAULT_KIND", Unit: "shared/declare_first",
			Value: func(l *Limits) string { return l.SavedData.DefaultKind },
			set: func(l *Limits, v string) error {
				switch strings.ToLower(v) {
				case DefaultKindShared, DefaultKindDeclareFirst:
					l.SavedData.DefaultKind = strings.ToLower(v)
					return nil
				}
				return fmt.Errorf("SAVED_DATA_DEFAULT_KIND=%q: want shared or declare_first", v)
			}},

		boolKnob("ASK_ENABLED", func(l *Limits) *bool { return &l.Ask.Enabled }),
		intKnob("ASK_BURST", "questions", 1, 50, func(l *Limits) *int { return &l.Ask.Burst }),
		intKnob("ASK_EVERY_SECONDS", "seconds", 1, 3600, func(l *Limits) *int { return &l.Ask.EverySeconds }),
		intKnob("ASK_DAILY_MAX", "questions", 0, 100_000, func(l *Limits) *int { return &l.Ask.DailyMax }),
		intKnob("ASK_MAX_IN_FLIGHT", "questions", 1, 32, func(l *Limits) *int { return &l.Ask.MaxInFlight }),
		{Env: "ASK_MODEL", Unit: "model name",
			Value: func(l *Limits) string { return l.Ask.Model },
			set: func(l *Limits, v string) error {
				if !askModelName.MatchString(v) {
					return fmt.Errorf("ASK_MODEL=%q: want a model name the model backend knows, like grok-4.7 (letters, digits, and . _ : / -, at most 100)", v)
				}
				l.Ask.Model = v
				return nil
			}},
		{Env: "ASK_REASONING_EFFORT", Unit: "none/low/medium/high",
			Value: func(l *Limits) string { return l.Ask.ReasoningEffort },
			set: func(l *Limits, v string) error {
				switch v = strings.ToLower(v); v {
				case "none", "low", "medium", "high":
					l.Ask.ReasoningEffort = v
					return nil
				}
				return fmt.Errorf("ASK_REASONING_EFFORT=%q: want none, low, medium or high", v)
			}},
		intKnob("ASK_MAX_TOKENS", "tokens", 50, 4000, func(l *Limits) *int { return &l.Ask.MaxTokens }),
		intKnob("SETUP_CHECK_DAILY_MAX", "checks", 0, 100_000, func(l *Limits) *int { return &l.Ask.SetupCheckDailyMax }),
		intKnob("SETUP_CHECK_MAX_IN_FLIGHT", "checks", 1, 64, func(l *Limits) *int { return &l.Ask.SetupCheckMaxInFlight }),
		intKnob("SETUP_CHECK_PER_NETWORK_DAILY", "checks", 1, 100_000, func(l *Limits) *int { return &l.Ask.SetupCheckPerNetworkDaily }),
		intKnob("SETUP_ASSIST_DAILY_MAX", "messages", 0, 100_000, func(l *Limits) *int { return &l.Ask.SetupAssistDailyMax }),
		intKnob("SETUP_ASSIST_MAX_IN_FLIGHT", "messages", 1, 64, func(l *Limits) *int { return &l.Ask.SetupAssistMaxInFlight }),
		intKnob("SETUP_ASSIST_PER_NETWORK_DAILY", "messages", 1, 100_000, func(l *Limits) *int { return &l.Ask.SetupAssistPerNetworkDaily }),
	}
}

// LoadLimits reads every knob through getenv (os.Getenv in production). Unset
// or blank keeps the default. A value that does not parse or is out of range
// is an error naming the variable and the range, as is a combination that
// cannot work (an idle expiry longer than the session it slides inside).
func LoadLimits(getenv func(string) string) (Limits, error) {
	l := DefaultLimits()
	for _, k := range Knobs() {
		v := strings.TrimSpace(getenv(k.Env))
		if v == "" {
			continue
		}
		if err := k.set(&l, v); err != nil {
			return DefaultLimits(), err
		}
	}
	switch {
	case l.VisitorSessionIdle > l.VisitorSessionTTL:
		return DefaultLimits(), fmt.Errorf("VISITOR_SESSION_IDLE_DAYS (%d) must not be longer than VISITOR_SESSION_DAYS (%d)",
			l.VisitorSessionIdle/day, l.VisitorSessionTTL/day)
	case l.DomainLapseAfter <= l.DomainLapseWarnAfter:
		return DefaultLimits(), fmt.Errorf("DOMAIN_LAPSE_HOURS (%d) must be longer than DOMAIN_LAPSE_WARN_HOURS (%d): the owner is warned before the domain is released",
			l.DomainLapseAfter/time.Hour, l.DomainLapseWarnAfter/time.Hour)
	case l.DomainUnprovenMaxAge < l.DomainUnprovenTTL:
		return DefaultLimits(), fmt.Errorf("DOMAIN_UNPROVEN_MAX_DAYS (%d days) must not be shorter than DOMAIN_UNPROVEN_HOURS (%d hours)",
			l.DomainUnprovenMaxAge/day, l.DomainUnprovenTTL/time.Hour)
	case l.AIMaxJobsPerUser > l.AIMaxJobs:
		return DefaultLimits(), fmt.Errorf("AI_MAX_JOBS_PER_USER (%d) must not be more than AI_MAX_JOBS (%d)", l.AIMaxJobsPerUser, l.AIMaxJobs)
	case l.SavedData.WatchKeepDays < l.SavedData.WatchDays:
		return DefaultLimits(), fmt.Errorf("SAVED_DATA_WATCH_KEEP_DAYS (%d) must not be shorter than SAVED_DATA_WATCH_DAYS (%d): the watch reads that many days of counts",
			l.SavedData.WatchKeepDays, l.SavedData.WatchDays)
	}
	return l, nil
}

func parseRange(env, v string, min, max int64) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s=%q: want a whole number from %d to %d", env, v, min, max)
	}
	return n, nil
}

// Rate bounds: a burst from 1 to 100,000; a refill interval from 1ms to a day.
const (
	rateMaxBurst = 100_000
	rateMinEvery = time.Millisecond
	rateMaxEvery = day
)

func parseRate(env, v string) (Rate, error) {
	bad := fmt.Errorf("%s=%q: want \"<burst>,<every>\", a burst from 1 to %d and a Go duration from 1ms to 24h, e.g. \"20,5s\" (20 at once, then one more every 5 seconds)",
		env, v, rateMaxBurst)
	b, e, ok := strings.Cut(v, ",")
	if !ok {
		return Rate{}, bad
	}
	burst, err := strconv.Atoi(strings.TrimSpace(b))
	if err != nil || burst < 1 || burst > rateMaxBurst {
		return Rate{}, bad
	}
	every, err := time.ParseDuration(strings.TrimSpace(e))
	if err != nil || every < rateMinEvery || every > rateMaxEvery {
		return Rate{}, bad
	}
	return Rate{Burst: burst, Every: every}, nil
}

// Warnings are startup notes about settings that load but deserve a second
// look: a rate limit loosened more than warnLoosen times past its default,
// and a RATE_LIMIT_* variable in environ ("NAME=value" lines, os.Environ in
// production) that is not a setting at all, most likely a typo, whose
// intended limit is therefore not in force.
func (l Limits) Warnings(environ []string) []string {
	def := DefaultLimits()
	known := map[string]bool{}
	var out []string
	for _, k := range Knobs() {
		known[k.Env] = true
		if k.Unit != "burst,every" || k.Sensitive {
			continue
		}
		r, _ := parseRate(k.Env, k.Value(&l))
		d, _ := parseRate(k.Env, k.Value(&def))
		if r.Burst > d.Burst*warnLoosen || r.Every < d.Every/warnLoosen {
			out = append(out, fmt.Sprintf("%s=%s is more than %d times looser than the default %s", k.Env, r, warnLoosen, d))
		}
	}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "RATE_LIMIT_") && !known[name] {
			out = append(out, name+" is not a setting Simple Host knows, so it changes nothing (see docs/configuration.md for the names)")
		}
	}
	return out
}

// Changed lists "ENV=value" for every knob that differs from its default, for
// the startup log.
func (l Limits) Changed() []string {
	def := DefaultLimits()
	var out []string
	for _, k := range Knobs() {
		if v := k.Value(&l); v != k.Value(&def) {
			out = append(out, k.Env+"="+v)
		}
	}
	return out
}

var active atomic.Pointer[Limits]

func init() {
	l := DefaultLimits()
	active.Store(&l)
}

// Active returns the limits in force. Before SetActive it is the defaults, so
// tests and tools that never load config see today's behaviour.
func Active() *Limits { return active.Load() }

// SetActive installs l as the limits in force. handler.ApplyLimits calls it,
// once before serving and in tests that try a changed knob.
func SetActive(l Limits) { active.Store(&l) }

// limitsFromEnv is Load's hook, split out so the test can call LoadLimits with
// a fake environment instead.
func limitsFromEnv() (Limits, error) { return LoadLimits(os.Getenv) }

// Count words a number of things the way the product's copy does: "one day",
// "7 days", "50 keys".
func Count(n int, unit string) string {
	if n == 1 {
		return "one " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// Span words a duration in the largest whole unit: "15 minutes", "one hour",
// "7 days".
func Span(d time.Duration) string {
	n, unit := spanUnit(d)
	return Count(n, unit)
}

// SpanAdj is Span as an adjective: "15-minute", "7-day", "one-hour".
func SpanAdj(d time.Duration) string {
	n, unit := spanUnit(d)
	if n == 1 {
		return "one-" + unit
	}
	return strconv.Itoa(n) + "-" + unit
}

func spanUnit(d time.Duration) (int, string) {
	switch {
	case d >= day && d%day == 0:
		return int(d / day), "day"
	case d >= time.Hour && d%time.Hour == 0:
		return int(d / time.Hour), "hour"
	case d >= time.Minute && d%time.Minute == 0:
		return int(d / time.Minute), "minute"
	default:
		return int(d / time.Second), "second"
	}
}
