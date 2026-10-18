// Package wpimport turns a WordPress export (WXR XML, the person's own media
// archive, optionally their live theme's stylesheets) into a static Simple
// Host site, deterministically and with no AI. Dynamic WordPress features
// (forms, comments, guestbook, forum, subscribe, search, hit counter, shop)
// come back in the Report so the connector can wire each to a Simple Host
// feature.
//
// The caller (internal/mcp, the import link) owns everything that touches the
// network or the request: it streams uploads to disk, validates archives, and
// passes a Fetch function that only reaches public addresses on the site's own
// hosts. This package never dials out on its own.
package wpimport

import (
	"context"
	"io"
)

// Input is one import job.
type Input struct {
	// Site is the Simple Host site name, written into window.SH_CONFIG.
	Site string
	// WXR holds one or more WXR documents (a large WordPress.com export is a
	// ZIP with several XML files; they describe one site and are merged).
	WXR []io.Reader
	// Media is the person's media archive, already unpacked and validated by
	// the caller, or nil. Keys are slash-separated relative paths as they
	// appear in the archive (for example "2026/07/photo.jpg" or
	// "wp-content/uploads/2026/07/photo.jpg").
	Media map[string]MediaFile
	// Fetch, when set, downloads a public media file or stylesheet. The
	// caller enforces scheme, host allowlist, public IPs, redirects, size and
	// time limits; an error means "not available", never fatal.
	Fetch func(ctx context.Context, url string) ([]byte, error)
	// ThemeURL, when set, is the live site's address: the importer fetches
	// that one page through Fetch to copy the look (stylesheets and inline
	// theme styles) and to find a static front page.
	ThemeURL string
	Limits   Limits
	// Progress, when set, is called as work advances (stage is a short plain
	// phrase such as "Reading your export" or "Shrinking photos").
	Progress func(stage string, done, total int)
}

// MediaFile opens one file from the person's media archive.
type MediaFile struct {
	Size int64
	Open func() (io.ReadCloser, error)
}

// Limits bound one import. Zero values take the defaults in DefaultLimits.
type Limits struct {
	MaxXMLBytes      int64 // total WXR bytes read
	MaxItems         int   // WXR items
	MaxSiteBytes     int64 // whole output; photos are shrunk further to fit
	MaxImagePixels   int64 // larger images are kept only if already small in bytes, else skipped
	ImageMaxEdge     int   // longest side of shrunk photos, in pixels
	JPEGQuality      int
	MaxMediaFileSize int64 // one media file, before shrinking
	FetchConcurrency int
}

// DefaultLimits are the values used for zero fields.
var DefaultLimits = Limits{
	MaxXMLBytes:      100 << 20,
	MaxItems:         20000,
	MaxSiteBytes:     190 << 20,
	MaxImagePixels:   60_000_000,
	ImageMaxEdge:     1600,
	JPEGQuality:      82,
	MaxMediaFileSize: 40 << 20,
	FetchConcurrency: 4,
}

// Result is the static site and what the importer found.
type Result struct {
	// Files maps a relative path ("index.html", "about/index.html",
	// "wp-content/uploads/2026/07/photo.jpg") to its bytes.
	Files  map[string][]byte
	Report Report
}

// Report is returned to the connector (get_import_report) and, in short, to
// the upload page. Every string is plain words.
type Report struct {
	SiteTitle string `json:"site_title"`
	// Platform is "Squarespace", "WordPress.com", or "WordPress".
	Platform    string `json:"platform,omitempty"`
	SourceURL   string `json:"source_url"` // the WXR base_site_url
	FrontPage   string `json:"front_page"` // "page:<path>" or "posts"
	Pages       int    `json:"pages"`
	Posts       int    `json:"posts"`
	OtherItems  int    `json:"other_items"`
	Categories  int    `json:"categories"`
	Tags        int    `json:"tags"`
	MenuItems   int    `json:"menu_items"`
	Comments    int    `json:"comments_kept"` // approved comments rendered as static text
	MediaCopied int    `json:"media_copied"`
	MediaUnused int    `json:"media_unused"` // attachments not referenced by the site
	// MediaMissing lists media URLs that were neither in the archive nor
	// fetchable; those keep their original address.
	MediaMissing []string `json:"media_missing,omitempty"`
	TotalBytes   int64    `json:"total_bytes"`
	ThemeCopied  bool     `json:"theme_copied"`
	// Features are the dynamic parts, one entry per kind found.
	Features []Feature `json:"features"`
	// Dropped names WordPress parts left out on purpose (likes, share
	// buttons, related posts, unknown shortcodes or dynamic blocks), with counts.
	Dropped  map[string]int `json:"dropped,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
}

// Feature kinds.
const (
	FeatureContactForm = "contact_form"
	FeatureComments    = "comments"
	FeatureGuestbook   = "guestbook"
	FeatureForum       = "forum"
	FeatureSubscribe   = "subscribe"
	FeatureSearch      = "search"
	FeatureHitCounter  = "hit_counter"
	FeatureShop        = "shop"
	FeatureDonation    = "donation"
)

// Feature statuses.
const (
	// StatusNeedsSetup: the page code is in place and waits for the storage
	// resource named in Setup; until then it says it is not connected yet.
	StatusNeedsSetup = "needs_setup"
	// StatusBuiltIn: works with no further step (search).
	StatusBuiltIn = "built_in"
	// StatusReplaced: removed from the pages; Simple Host covers it another
	// way (hit counter: site_analytics).
	StatusReplaced = "replaced"
	// StatusNotSupported: shown as static content only (shop checkout).
	StatusNotSupported = "not_supported"
)

// Feature is one dynamic WordPress feature found in the export.
type Feature struct {
	Kind   string   `json:"kind"`
	Status string   `json:"status"`
	Pages  []string `json:"pages"`           // site paths, "/contact/"
	Source string   `json:"source"`          // what it was: "Contact Form 7", "Jetpack form", "Gwolle guestbook"
	Count  int      `json:"count,omitempty"` // forms, existing comments, products
	// Moderated: the old site held posts for approval (comments with
	// wp:comment_approved 0), so approve_first is suggested.
	Moderated bool `json:"moderated,omitempty"`
	// Use names the Simple Host feature in plain words, Recipe the
	// get_page_recipe topic.
	Use    string `json:"use"`
	Recipe string `json:"recipe,omitempty"`
	// Setup lists the exact connector calls that switch it on.
	Setup []ToolCall `json:"setup,omitempty"`
	Note  string     `json:"note,omitempty"`
}

// ToolCall is one connector tool call with its arguments.
type ToolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// Convert runs one import. It returns an error only when nothing usable can
// be built (no readable WXR, no published pages or posts, over a hard limit);
// everything else lands in Report.Warnings.
func Convert(ctx context.Context, in Input) (*Result, error) {
	return convert(ctx, in)
}
