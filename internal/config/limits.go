package config

import (
	"fmt"
	"os"
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
	RateDomainCheck     Rate // RATE_LIMIT_DOMAIN_CHECK
	RateDomainCheckUser Rate // RATE_LIMIT_DOMAIN_CHECK_USER
	RateOAuthRegister   Rate // RATE_LIMIT_OAUTH_REGISTER
	RateOAuthAuthorize  Rate // RATE_LIMIT_OAUTH_AUTHORIZE
	RateOAuthToken      Rate // RATE_LIMIT_OAUTH_TOKEN
	RateAIIP            Rate // RATE_LIMIT_AI_IP
	RateAIUser          Rate // RATE_LIMIT_AI_USER
	RateTranscribe      Rate // RATE_LIMIT_TRANSCRIBE
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
		RateDomainCheck:     Rate{10, 10 * time.Second},
		RateDomainCheckUser: Rate{3, 30 * time.Second},
		RateOAuthRegister:   Rate{10, 6 * time.Minute},
		RateOAuthAuthorize:  Rate{30, 2 * time.Second},
		RateOAuthToken:      Rate{30, 2 * time.Second},
		RateAIIP:            Rate{20, 12 * time.Second},
		RateAIUser:          Rate{30, 10 * time.Second},
		RateTranscribe:      Rate{60, 3 * time.Second},
	}
}

// Knob describes one setting for the docs table and the startup log.
type Knob struct {
	Env, Unit string
	Min, Max  int64
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

func rateKnob(env string, p func(*Limits) *Rate) Knob {
	return Knob{Env: env, Unit: "burst,every",
		Value: func(l *Limits) string { return p(l).String() },
		set: func(l *Limits, v string) error {
			r, err := parseRate(env, v)
			*p(l) = r
			return err
		}}
}

// Knobs lists every setting, in the order docs/configuration.md gives them.
func Knobs() []Knob {
	m, h, d := time.Minute, time.Hour, day
	return []Knob{
		durKnob("SIGNIN_CODE_TTL_MINUTES", "minutes", m, 5, 60, func(l *Limits) *time.Duration { return &l.SigninCodeTTL }),
		intKnob("MAX_KEYS_PER_ACCOUNT", "keys", 1, 1000, func(l *Limits) *int { return &l.MaxKeysPerAccount }),
		durKnob("HANDLE_RENAME_EVERY_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.HandleRenameEvery }),
		durKnob("EMAIL_CHANGE_UNDO_DAYS", "days", d, 1, 90, func(l *Limits) *time.Duration { return &l.EmailChangeUndoTTL }),

		intKnob("MAX_SITES_PER_ACCOUNT", "sites", 1, 100_000, func(l *Limits) *int { return &l.MaxSitesPerAccount }),
		intKnob("MAX_FILES_PER_SITE", "files", 100, 500_000, func(l *Limits) *int { return &l.MaxFilesPerSite }),
		durKnob("PREVIEW_LINK_TTL_MINUTES", "minutes", m, 5, 7*24*60, func(l *Limits) *time.Duration { return &l.PreviewLinkTTL }),
		durKnob("EXPORT_LINK_TTL_MINUTES", "minutes", m, 1, 24*60, func(l *Limits) *time.Duration { return &l.ExportLinkTTL }),

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
		durKnob("EVENT_TTL_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.EventTTL }),
		intKnob("EVENT_MAX_CLAIMS", "names", 1, 100, func(l *Limits) *int { return &l.EventMaxClaims }),

		durKnob("DELETED_RETENTION_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.DeletedRetention }),
		durKnob("IDLE_AFTER_DAYS", "days", d, 7, 3650, func(l *Limits) *time.Duration { return &l.IdleAfter }),
		durKnob("IDLE_GRACE_DAYS", "days", d, 1, 365, func(l *Limits) *time.Duration { return &l.IdleGrace }),
		intKnob("ANALYTICS_RETENTION_DAYS", "days", 1, 3650, func(l *Limits) *int { return &l.AnalyticsRetention }),
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

		rateKnob("RATE_LIMIT_SIGNIN_IP", func(l *Limits) *Rate { return &l.RateSigninIP }),
		rateKnob("RATE_LIMIT_SIGNIN_EMAIL", func(l *Limits) *Rate { return &l.RateSigninEmail }),
		rateKnob("RATE_LIMIT_VISITOR_OAUTH", func(l *Limits) *Rate { return &l.RateVisitorOAuth }),
		rateKnob("RATE_LIMIT_VISITOR_AUTH", func(l *Limits) *Rate { return &l.RateVisitorAuth }),
		rateKnob("RATE_LIMIT_VISITOR", func(l *Limits) *Rate { return &l.RateVisitor }),
		rateKnob("RATE_LIMIT_UPLOAD", func(l *Limits) *Rate { return &l.RateUpload }),
		rateKnob("RATE_LIMIT_STATE", func(l *Limits) *Rate { return &l.RateState }),
		rateKnob("RATE_LIMIT_SITE_OPS", func(l *Limits) *Rate { return &l.RateSiteOps }),
		rateKnob("RATE_LIMIT_EXPORT", func(l *Limits) *Rate { return &l.RateExport }),
		rateKnob("RATE_LIMIT_DOMAIN_CHECK", func(l *Limits) *Rate { return &l.RateDomainCheck }),
		rateKnob("RATE_LIMIT_DOMAIN_CHECK_USER", func(l *Limits) *Rate { return &l.RateDomainCheckUser }),
		rateKnob("RATE_LIMIT_OAUTH_REGISTER", func(l *Limits) *Rate { return &l.RateOAuthRegister }),
		rateKnob("RATE_LIMIT_OAUTH_AUTHORIZE", func(l *Limits) *Rate { return &l.RateOAuthAuthorize }),
		rateKnob("RATE_LIMIT_OAUTH_TOKEN", func(l *Limits) *Rate { return &l.RateOAuthToken }),
		rateKnob("RATE_LIMIT_AI_IP", func(l *Limits) *Rate { return &l.RateAIIP }),
		rateKnob("RATE_LIMIT_AI_USER", func(l *Limits) *Rate { return &l.RateAIUser }),
		rateKnob("RATE_LIMIT_TRANSCRIBE", func(l *Limits) *Rate { return &l.RateTranscribe }),
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
