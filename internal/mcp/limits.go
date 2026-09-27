package mcp

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Limits are the operational times the tool descriptions, instructions and
// results state. They come from internal/config (docs/configuration.md);
// handler.ApplyLimits installs them at startup. The zero state is today's
// values.
type Limits struct {
	DeletedRetention     time.Duration // DELETED_RETENTION_DAYS
	PreviewLinkTTL       time.Duration // PREVIEW_LINK_TTL_MINUTES
	ExportLinkTTL        time.Duration // EXPORT_LINK_TTL_MINUTES
	IdleAfter            time.Duration // IDLE_AFTER_DAYS
	IdleGrace            time.Duration // IDLE_GRACE_DAYS
	DomainUnprovenTTL    time.Duration // DOMAIN_UNPROVEN_HOURS
	DomainLapseWarnAfter time.Duration // DOMAIN_LAPSE_WARN_HOURS
	DomainLapseAfter     time.Duration // DOMAIN_LAPSE_HOURS
	UndoDays             time.Duration // SAVED_DATA_UNDO_DAYS
}

// DefaultLimits is today's behaviour. internal/handler's tests check it
// matches config.DefaultLimits.
func DefaultLimits() Limits {
	const day = 24 * time.Hour
	return Limits{
		DeletedRetention:     7 * day,
		PreviewLinkTTL:       time.Hour,
		ExportLinkTTL:        10 * time.Minute,
		IdleAfter:            90 * day,
		IdleGrace:            30 * day,
		DomainUnprovenTTL:    24 * time.Hour,
		DomainLapseWarnAfter: 24 * time.Hour,
		DomainLapseAfter:     72 * time.Hour,
		UndoDays:             30 * day,
	}
}

var limits atomic.Pointer[Limits]

func init() { SetLimits(DefaultLimits()) }

// SetLimits installs l. Call at startup, before serving.
func SetLimits(l Limits) { limits.Store(&l) }

func lim() *Limits { return limits.Load() }

// span words a duration the way config.Span does ("7 days", "one hour"); this
// package may not import internal/config (scripts/check-layering.sh), and a
// handler test holds the two to the same output.
func span(d time.Duration) string {
	const day = 24 * time.Hour
	n, unit := int(d/time.Second), "second"
	switch {
	case d >= day && d%day == 0:
		n, unit = int(d/day), "day"
	case d >= time.Hour && d%time.Hour == 0:
		n, unit = int(d/time.Hour), "hour"
	case d >= time.Minute && d%time.Minute == 0:
		n, unit = int(d/time.Minute), "minute"
	}
	if n == 1 {
		return "one " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// spanAdj is span as an adjective: "30-day", "one-day".
func spanAdj(d time.Duration) string {
	n, unit, _ := strings.Cut(span(d), " ")
	unit = strings.TrimSuffix(unit, "s")
	return n + "-" + unit
}

// Span is span, exported for that test.
func Span(d time.Duration) string { return span(d) }
