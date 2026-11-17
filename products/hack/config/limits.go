package config

import (
	"fmt"
	platform "github.com/vsriram/simple-host/host/config"
	"strconv"
	"strings"
	"time"
)

type Rate = platform.Rate

const day = 24 * time.Hour

// Limits holds product limits beside the platform snapshot used by the same handler.
type Limits struct {
	platform.Limits
	EventLimitExemptRaw        string        // EVENT_LIMIT_EXEMPT
	EventCreatePerDay          int           // EVENT_CREATE_PER_DAY
	EventMaxActivePerOrganiser int           // EVENT_MAX_ACTIVE_PER_ORGANISER
	EventTeamSizeDefault       int           // EVENT_TEAM_SIZE_DEFAULT
	EventSitesKeep             time.Duration // EVENT_SITES_KEEP_DAYS (M4: team sites kept after the event closes)
	EventRemovalWarn           time.Duration // EVENT_REMOVAL_WARN_DAYS (M4: one warning this long before team sites are removed)
	HackInstanceBudgetGB       int           // HACK_INSTANCE_BUDGET_GB (0: no budget)
	HackEventIconMaxBytes      int           // HACK_EVENT_ICON_MAX_BYTES
	EventApplyPendingPerEvent  int           // EVENT_APPLY_PENDING_PER_EVENT
	RateEventCodesIP           Rate          // RATE_LIMIT_EVENT_CODES_IP
	RateEventCodesUser         Rate          // RATE_LIMIT_EVENT_CODES_USER
	RateEventNamesUser         Rate          // RATE_LIMIT_EVENT_NAMES_USER
	RateEventApplyIP           Rate          // RATE_LIMIT_EVENT_APPLY_IP
	RateEventApplyUser         Rate          // RATE_LIMIT_EVENT_APPLY_USER
}

func DefaultLimits() Limits {
	return Limits{Limits: platform.DefaultLimits(),
		EventCreatePerDay:          20,
		EventMaxActivePerOrganiser: 0,
		EventTeamSizeDefault:       20,
		EventSitesKeep:             30 * day,
		EventRemovalWarn:           14 * day,
		HackInstanceBudgetGB:       10,
		HackEventIconMaxBytes:      128 << 10,
		EventApplyPendingPerEvent:  500,
		RateEventCodesIP:           Rate{Burst: 120, Every: time.Second},
		RateEventCodesUser:         Rate{Burst: 20, Every: 3 * time.Second},
		RateEventNamesUser:         Rate{Burst: 60, Every: time.Second},
		RateEventApplyIP:           Rate{Burst: 30, Every: 2 * time.Minute},
		RateEventApplyUser:         Rate{Burst: 5, Every: 12 * time.Minute},
	}
}
func Knobs() []Knob {
	d := day
	return []Knob{
		{Env: "EVENT_LIMIT_EXEMPT", Unit: "emails and domains",
			Value: func(l *Limits) string { return l.EventLimitExemptRaw },
			set: func(l *Limits, v string) error {
				l.EventLimitExemptRaw = v
				return nil
			}},
		intKnob("EVENT_CREATE_PER_DAY", "events", 0, 1000, func(l *Limits) *int { return &l.EventCreatePerDay }),
		intKnob("EVENT_MAX_ACTIVE_PER_ORGANISER", "events", 0, 1000, func(l *Limits) *int { return &l.EventMaxActivePerOrganiser }),
		intKnob("EVENT_APPLY_PENDING_PER_EVENT", "applications", 1, 100_000, func(l *Limits) *int { return &l.EventApplyPendingPerEvent }),
		intKnob("EVENT_TEAM_SIZE_DEFAULT", "people", 1, 50, func(l *Limits) *int { return &l.EventTeamSizeDefault }),
		durKnob("EVENT_SITES_KEEP_DAYS", "days", d, 1, 3650, func(l *Limits) *time.Duration { return &l.EventSitesKeep }),
		durKnob("EVENT_REMOVAL_WARN_DAYS", "days", d, 1, 3650, func(l *Limits) *time.Duration { return &l.EventRemovalWarn }),
		intKnob("HACK_INSTANCE_BUDGET_GB", "GB", 0, 100_000, func(l *Limits) *int { return &l.HackInstanceBudgetGB }),
		intKnob("HACK_EVENT_ICON_MAX_BYTES", "bytes", 1, 4<<20, func(l *Limits) *int { return &l.HackEventIconMaxBytes }),
		rateKnob("RATE_LIMIT_EVENT_CODES_IP", func(l *Limits) *Rate { return &l.RateEventCodesIP }),
		rateKnob("RATE_LIMIT_EVENT_CODES_USER", func(l *Limits) *Rate { return &l.RateEventCodesUser }),
		rateKnob("RATE_LIMIT_EVENT_NAMES_USER", func(l *Limits) *Rate { return &l.RateEventNamesUser }),
		rateKnob("RATE_LIMIT_EVENT_APPLY_IP", func(l *Limits) *Rate { return &l.RateEventApplyIP }),
		rateKnob("RATE_LIMIT_EVENT_APPLY_USER", func(l *Limits) *Rate { return &l.RateEventApplyUser }),
	}
}
func LoadLimits(getenv func(string) string) (Limits, error) {
	l := DefaultLimits()
	for _, k := range Knobs() {
		if v := strings.TrimSpace(getenv(k.Env)); v != "" {
			if err := k.set(&l, v); err != nil {
				return DefaultLimits(), err
			}
		}
	}
	return l, nil
}

// Changed lists the product knobs that differ from their defaults for startup.
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

func (l Limits) Warnings() []string {
	def := DefaultLimits()
	var out []string
	for _, k := range Knobs() {
		if k.Unit != "burst,every" {
			continue
		}
		r, _ := parseRate(k.Env, k.Value(&l))
		d, _ := parseRate(k.Env, k.Value(&def))
		if r.Burst > d.Burst*10 || r.Every < d.Every/10 {
			out = append(out, fmt.Sprintf("%s=%s is more than %d times looser than the default %s", k.Env, r, 10, d))
		}
	}
	return out
}

// EventLimitExempt matches sign-in emails against EVENT_LIMIT_EXEMPT.
func (l Limits) EventLimitExempt(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	for _, entry := range strings.Split(l.EventLimitExemptRaw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == email || entry == email[at:] {
			return true
		}
	}
	return false
}

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

func rateKnob(env string, p func(*Limits) *Rate) Knob {
	return Knob{Env: env, Unit: "burst,every",
		Value: func(l *Limits) string { return p(l).String() },
		set: func(l *Limits, v string) error {
			r, err := parseRate(env, v)
			*p(l) = r
			return err
		}}
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
