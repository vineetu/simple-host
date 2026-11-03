package sites

import (
	"errors"
	"regexp"
	"strings"
)

// ValidSiteName matches a single DNS label: lowercase alphanumerics and
// hyphens, no leading/trailing hyphen, 1–63 chars. Site names become both a
// subdomain (`<name>.simple-host.app`) and an on-disk directory, and they flow
// into the cortex-share deploy queue, so this is the load-bearing input guard
// against path/queue/domain abuse. Every currently-deployed site name passes
// this pattern, so it is safe to enforce on both create and update.
var ValidSiteName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

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

// ValidateShape enforces the DNS-label charset. Safe to call on both create
// and update — it only rejects names no existing site uses.
func ValidateShape(name string) error {
	if !ValidSiteName.MatchString(name) {
		return errors.New("invalid site name: use 1–63 lowercase letters, digits, and hyphens (no leading/trailing hyphen)")
	}
	return nil
}

// ValidateReserved rejects names reserved for platform infrastructure.
// Call on CREATE only.
func ValidateReserved(name string) error {
	if _, reserved := reservedSiteNames[name]; reserved || reservedNewSiteNameSet[name] {
		return errors.New("this name is reserved; pick another")
	}
	return nil
}

// reservedHandles are path segments / product names that must never be claimed
// as user handles (they collide with API or site routes).
var reservedHandles = map[string]bool{
	"v1":          true,
	"internal":    true,
	"api":         true,
	"auth":        true,
	"me":          true,
	"sites":       true,
	"handles":     true,
	"by-id":       true,
	"static":      true,
	"assets":      true,
	"well-known":  true,
	"favicon.ico": true,
	"robots.txt":  true,
	"admin":       true,
	"root":        true,
	"www":         true,
	"app":         true,
	"dashboard":   true,
	"login":       true,
	"logout":      true,
	"health":      true,
	"healthz":     true,
	"readyz":      true,
	"skills":      true,
	"plugin":      true,
	"install":     true,
	"setup":       true,
	"openapi":     true,
	"llms.txt":    true,
}

// reservedSubdomainLabels is the single source of truth for names under the
// platform domain that no site may claim. It must contain every label nginx
// answers itself with an exact server_name (those never reach the app, so a
// claim there would be a binding that can never serve) plus the names the
// platform uses or may use. scripts/check-reserved-subdomains.sh compares it
// against the live nginx config where that is readable.
var reservedSubdomainLabels = []string{
	// Platform hosts.
	"www", "sites", "api", "admin", "mail", "cname", "app", "apex",
	"auth", "login", "oauth", "mcp", "connect", "dashboard", "console",
	"static", "assets", "cdn", "docs", "status", "help", "support", "blog",
	"smtp", "imap", "pop", "ftp", "ns", "ns1", "ns2", "mx", "email",
	"internal", "v1", "skills", "plugin", "install", "healthz", "readyz",
	"hack", "events", "test", "staging", "dev", "localhost",
	// Hand-configured nginx exact server_names on the live box (and the
	// wildcard *.lab block's parent name).
	"sf-fog", "gods-eye", "paragliding-beginners-map", "lab",
}

// ReservedSubdomainSet is every reserved name in the one shared namespace
// (owner decision 2026-09-25): the labels above plus every reserved handle,
// since a handle is also an address (<handle>.<SITE_DOMAIN>) and a claimed
// name is also a path segment on the legacy content host.
var ReservedSubdomainSet = func() map[string]bool {
	m := make(map[string]bool, len(reservedSubdomainLabels)+len(reservedHandles))
	for _, l := range reservedSubdomainLabels {
		m[l] = true
	}
	for l := range reservedHandles {
		m[l] = true
	}
	return m
}()

// LabelReserved reports whether a name is reserved in the shared namespace:
// no account may take it as a handle and no site may claim it.
func LabelReserved(label string) bool {
	return ReservedSubdomainSet[strings.ToLower(label)]
}

// reservedNewNames (owner decision 2026-09-27) are names that read as the
// platform, its operator or a sensitive function, so a stranger holding one
// could pass for Simple Host. They are refused to NEW claims only: a new or
// changed handle, a newly claimed <name>.<SITE_DOMAIN>, and (the subset in
// reservedNewSiteNames) a new or renamed site. Accounts and sites that
// already hold one keep it and keep working (it is not a serving check), so
// this list never goes into ReservedSubdomainSet. docs/advanced/
// server-and-addresses.md lists it; keep the two together.
var reservedNewNames = []string{
	"photos", "photo-codecs",
	"admin", "administrator", "root", "sys", "system", "support", "help",
	"helpdesk", "info", "contact", "hello", "security", "abuse", "postmaster",
	"hostmaster", "webmaster", "noreply", "no-reply", "mail", "email", "smtp",
	"www", "api", "app", "apps", "status", "billing", "payments", "pay",
	"login", "signin", "sign-in", "signup", "sign-up", "auth", "oauth", "sso",
	"account", "accounts", "dashboard", "console", "docs", "doc", "blog",
	"cdn", "static", "assets", "media", "files", "download", "downloads",
	"setup", "enterprise", "legal", "privacy", "terms", "report", "policy", "team",
	"staff", "official", "verify", "verification", "update", "secure",
	"simplehost", "simple-host", "simplehack", "simple-hack", "test", "dev",
	"staging", "prod", "internal", "localhost",
}

// reservedNewSiteNames is the part of reservedNewNames that impersonates the
// platform or its operator. A site name only ever appears under its owner's
// address (<site>.<handle>.<SITE_DOMAIN>), so everyday names such as blog,
// docs, team or test stay free for sites.
var reservedNewSiteNames = []string{
	"admin", "administrator", "root", "sys", "system", "support", "helpdesk",
	"security", "abuse", "postmaster", "hostmaster", "webmaster", "noreply",
	"no-reply", "billing", "payments", "login", "signin", "sign-in", "signup",
	"sign-up", "auth", "oauth", "sso", "account", "accounts", "verify",
	"verification", "secure", "official", "simplehost", "simple-host",
	"simplehack", "simple-hack", "internal", "localhost",
}

func nameSet(names ...[]string) map[string]bool {
	m := map[string]bool{}
	for _, l := range names {
		for _, n := range l {
			m[n] = true
		}
	}
	return m
}

var (
	reservedNewNameSet     = nameSet(reservedNewNames)
	reservedNewSiteNameSet = nameSet(reservedNewSiteNames)
)

// LabelReservedForNew reports whether a NEW handle or claimed name may not
// be label: reserved in the shared namespace, or one of reservedNewNames.
func LabelReservedForNew(label string) bool {
	l := strings.ToLower(label)
	return ReservedSubdomainSet[l] || reservedNewNameSet[l]
}

// HandleIsLabel reports whether handle is usable as a DNS label, since it is
// also the account's address (<handle>.<SITE_DOMAIN>).
func HandleIsLabel(handle string) bool {
	return handle != "" && handle[0] != '-' && handle[len(handle)-1] != '-' && !strings.HasPrefix(handle, "xn--")
}

// VisitorHandle / visitorSitenameRe (internal/handler/oauth.go) are the
// path-model segments allowed in return_to (SPEC §2.1).
var VisitorHandle = regexp.MustCompile(`^[a-z0-9-]{1,39}$`)

// UUIDShape guards id path values before they reach a uuid column.
var UUIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
