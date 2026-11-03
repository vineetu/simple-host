package handler

import (
	rulesauth "github.com/vsriram/simple-host/internal/rules/auth"
	rulesstorage "github.com/vsriram/simple-host/internal/rules/storage"

	"github.com/vsriram/simple-host/internal/httpx"
)

// Transitional (cleanup 3.2): callers move to httpx/rules/auth/rules/storage
// in 4.x.
//
// This file re-exposes the Host-side helpers that moved out of
// opaque_tokens.go, connector.go and site.go under the names internal/handler
// used before the move, so existing call sites (including
// internal/handler/hack*.go, owned by a parallel worker in this step) keep
// compiling unchanged.

// sealLink/openLink are the generic signed-link primitive (opaque_tokens.go),
// now internal/httpx.SealLink/OpenLink: it has no Host-specific logic, and
// Hack's own archive-claim links (hack_exportlink.go) use it the same way.
var (
	sealLink = httpx.SealLink
	openLink = httpx.OpenLink
)

// cleanClientName is the OAuth-client display-name sanitizer
// (connector.go), now internal/httpx.CleanClientName.
var cleanClientName = httpx.CleanClientName

// oauthScope/hackEventsScope are connector.go's grant-scope constants, now
// held by internal/rules/auth alongside the classification logic that reads
// them (GrantKind) so the two never drift apart.
const (
	oauthScope      = rulesauth.OAuthScope
	hackEventsScope = rulesauth.HackEventsScope
)

// scopeTeam/hackGrantKind are connector.go's grant-scope decisions, now
// internal/rules/auth.ScopeTeam/GrantKind: pure string/const logic over an
// OAuth scope string, no SQL or HTTP.
var (
	scopeTeam     = rulesauth.ScopeTeam
	hackGrantKind = rulesauth.GrantKind
)

// extractBudget is site.go's tarball extraction budget, now
// internal/rules/storage.ExtractBudget: pure arithmetic over a limit its
// caller has already resolved (h.siteLimitFor), shared with the Hack
// website-upload path (hack_websites.go).
var extractBudget = rulesstorage.ExtractBudget
