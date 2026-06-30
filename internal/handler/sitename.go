package handler

import (
	"errors"
	"regexp"
)

// validSiteName matches a single DNS label: lowercase alphanumerics and
// hyphens, no leading/trailing hyphen, 1–63 chars. Site names become both a
// subdomain (`<name>.simple-host.app`) and an on-disk directory, and they flow
// into the cortex-share deploy queue, so this is the load-bearing input guard
// against path/queue/domain abuse. Every currently-deployed site name passes
// this pattern, so it is safe to enforce on both create and update.
var validSiteName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedSiteNames are names that must not be claimable by a new site because
// they collide with platform infrastructure or critical service domains.
// IMPORTANT: this denylist is enforced on CREATE only. Existing sites (some of
// which — e.g. revenue-facing ones — may share a prefix with a service) must
// always remain re-deployable, so updateSite never consults this list.
var reservedSiteNames = map[string]struct{}{
	"simple-host": {},
	"jot-webhook": {},
	"www":         {},
	"api":         {},
	"admin":       {},
	"app":         {},
	"static":      {},
	"assets":      {},
	"internal":    {},
	"localhost":   {},
}

// validateSiteShape enforces the DNS-label charset. Safe to call on both create
// and update — it only rejects names no existing site uses.
func validateSiteShape(name string) error {
	if !validSiteName.MatchString(name) {
		return errors.New("invalid site name: use 1–63 lowercase letters, digits, and hyphens (no leading/trailing hyphen)")
	}
	return nil
}

// validateSiteReserved rejects names reserved for platform infrastructure.
// Call on CREATE only.
func validateSiteReserved(name string) error {
	if _, reserved := reservedSiteNames[name]; reserved {
		return errors.New("site name is reserved")
	}
	return nil
}
