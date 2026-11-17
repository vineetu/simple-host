// Package brain is the owner's product knowledge base: what Simple Host,
// Simple Hack and Simple Host Enterprise are for, every feature, parity across
// editions, dated decisions and recent changes, built from an allowlist of
// documents in the two repos.
//
// Build (run by the sh-brain-sync timer through `simple-host brain-build`)
// reads the allowlisted files from two checkouts and writes one JSON bundle.
// Store serves queries from that bundle and reloads it when the file changes,
// so a rebuild needs no restart. Nothing here reads the filesystem at query
// time except the bundle itself: get_doc can only return a document that the
// builder put in the bundle, so no request path can reach another file.
//
// The package is a leaf: it imports nothing else from the module.
package brain

import "time"

// Format is the bundle layout version. Store refuses a bundle of another one.
const Format = 1

// Bundle is the whole knowledge base, as written to bundle.json.
type Bundle struct {
	Format    int         `json:"format"`
	BuiltAt   time.Time   `json:"built_at"`
	Sources   []Source    `json:"sources"`
	Docs      []Doc       `json:"docs"`
	Products  []Product   `json:"products"`
	Parity    []ParityRow `json:"parity"`
	Decisions []Decision  `json:"decisions"`
	Changes   []Change    `json:"changes"`
	// ParityCopiesMatch is false when the two repos' PARITY.md differ (the
	// rule is that they are identical); the hosted copy is the one parsed.
	ParityCopiesMatch bool `json:"parity_copies_match"`
}

// Source is one repo the bundle was built from.
type Source struct {
	Label      string    `json:"label,omitempty"`
	Repo       string    `json:"repo"`
	Commit     string    `json:"commit"`
	CommitTime time.Time `json:"commit_time"`
}

// Doc is one allowlisted document, split at its headings.
type Doc struct {
	Label    string    `json:"label,omitempty"`
	Path     string    `json:"path"` // "<repo>/<file>", e.g. simple-host/INTENT.md
	Repo     string    `json:"repo"`
	Title    string    `json:"title"`
	Sections []Section `json:"sections"`
}

// Section is the text under one heading, up to the next heading of any level.
type Section struct {
	ID      string `json:"id"` // GitHub-style anchor, unique within the doc
	Heading string `json:"heading"`
	Level   int    `json:"level"`
	Text    string `json:"text"`
}

// Product is one product or edition's identity.
type Product struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	What     string   `json:"what"`
	WhoFor   string   `json:"who_for"`
	Success  string   `json:"success"`
	NonGoals []string `json:"non_goals"`
	Version  string   `json:"version"`
	Sources  []string `json:"sources"`
}

// ParityRow is one row of PARITY.md's feature table.
type ParityRow struct {
	Area       string `json:"area"`
	Hosted     string `json:"hosted"`
	Enterprise string `json:"enterprise"`
	Status     string `json:"status"`
	// State is Status reduced to same, different_on_purpose, gap_hosted,
	// gap_enterprise, not_applicable or other.
	State string `json:"state"`
	Date  string `json:"date,omitempty"`
}

// Decision is one dated entry from an INTENT.md.
type Decision struct {
	Date    string `json:"date"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Repo    string `json:"repo"`
	Source  string `json:"source"` // path#section
	Product string `json:"product"`
}

// Change is one CHANGELOG.md entry (a dated heading and its lines).
type Change struct {
	Date    string   `json:"date"` // "" for Unreleased
	Title   string   `json:"title"`
	Items   []string `json:"items"`
	Repo    string   `json:"repo"`
	Product string   `json:"product"`
	Source  string   `json:"source"`
}
