package web

import (
	"errors"
)

// Look before it goes live.
//
// A deploy with ?publish=false stores a new version without making it live:
// `current`, active_version and what visitors see do not change. Any kept
// version (stored that way, live now or live before) can be opened by its
// owner through a preview link on the site's own host:
//
//	https://<site>.<handle>.<SITE_DOMAIN>/__preview/<n>/<token>/...
//
// (or <handle>.<SITE_DOMAIN>/<site>/__preview/... while the person's
// certificate is not ready). The token encrypts and authenticates (site, version,
// expiry) with the per-process key export links use, under its own domain
// string; it opens that one version of that one site for previewLinkTTL, and
// stops working when the server restarts. Preview pages are noindex and never
// cached, and saves made from them are refused, so trying a new version never
// writes into the live site's data. Making the version live is the existing
// rollback (PUT .../active-version).

// PreviewLinkDomain separates these tokens from export links and anything else
// the key could be asked to sign.
const PreviewLinkDomain = "simple-host version preview v1"

var errPreviewLink = errors.New("this preview link is not valid or has expired")
