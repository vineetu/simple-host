package mcp

import (
	"errors"
	"strconv"
	"strings"
)

// The owner's product knowledge server (/brain/mcp on simple-host.app): a
// second Server with its own read-only tools and instructions. It shares the
// protocol handling above and nothing else; who may reach it is decided by
// the handler before ServeHTTP (the platform admin only).

// BrainQuery answers one brain tool by name. An ArgError-like error (any
// error) comes back to the model as a failed tool result it can correct.
type BrainQuery func(name string, args map[string]any) (text string, structured map[string]any, err error)

// BrainConfig wires the knowledge server.
type BrainConfig struct {
	ServerName string
	Version    string
	Query      BrainQuery
}

// inventory is a fixed tool set for a Server that is not the site connector.
type inventory struct {
	tools        []Tool
	bare         []Tool
	byName       map[string]Tool
	instructions string
	title        string
}

// NewBrainServer builds the knowledge server. Unlike NewServer it sets no
// package state, so it can live beside the site connector.
func NewBrainServer(cfg BrainConfig) *Server {
	if cfg.Query == nil {
		panic("mcp: brain server needs a query function")
	}
	tools := BrainTools(cfg.Query)
	inv := &inventory{byName: map[string]Tool{}, instructions: BrainInstructions, title: "Simple Host product knowledge"}
	for _, t := range tools {
		inv.tools = append(inv.tools, t)
		bare := t
		bare.OutputSchema = nil
		inv.bare = append(inv.bare, bare)
		inv.byName[t.Name] = t
	}
	return &Server{cfg: Config{ServerName: cfg.ServerName, Version: cfg.Version, MaxBodyBytes: 1 << 20}, fixed: inv}
}

// BrainInstructions tells the agent what the server is and when to use it.
const BrainInstructions = `This is the owner's private product knowledge base for Simple Host (simple-host.app), Simple Hack (simple-hack.app), Simple Host Enterprise, the self-hosted editions (the Simple Host small box and the Simple Hack standalone package) and Simple Hack for companies. It is built every hour, and after every deploy, from the INTENT, FEATURES, PARITY and CHANGELOG files of both repos plus design, platform and release docs. Only the owner can reach it, and it only reads.

Call these tools BEFORE answering any question about these products, instead of answering from memory: what a product is for, whether a feature exists or how it works, which edition has it, what was decided and why, and what changed recently. Quote what the tools return and name the source file; if the tools do not say, say that rather than guessing.

Which tool:
- product_overview: what a product is, who it is for, non-goals, current version, and how fresh this knowledge is.
- feature_lookup: does a feature exist, how it works, where it lives, plus its parity row.
- parity: which edition has a feature (same, different on purpose, gap), or the whole table.
- decisions: dated owner decisions with reasons; filter by date, words or product.
- recent_changes: changelog entries since a date or a span like 3d.
- search: full-text search over every source, with snippets.
- get_doc: read one source document or one section of it (paths come from the other tools).

Parity terms: "hosted" means simple-host.app and the small box (the same binary, which also runs Simple Hack); "enterprise" is the Kubernetes edition. "gap → enterprise" means Enterprise should get it; "gap → hosted" means the hosted side should.`

func brainTool(name, title, description string, input, outSchema map[string]any, q BrainQuery) Tool {
	return Tool{
		Name: name, Title: title, Description: description,
		InputSchema: input, OutputSchema: outSchema, Annotations: readOnly(),
		run: func(_ *call, args map[string]any) (output, error) {
			text, structured, err := q(name, args)
			if err != nil {
				return output{}, err
			}
			if structured == nil {
				return output{}, errors.New("no answer")
			}
			return output{Text: text, Structured: structured}, nil
		},
	}
}

var brainProducts = []string{"host", "hack", "enterprise", "selfhost-host", "selfhost-hack", "hack-companies", "console", "travel", "all"}

func brainParityRowSchema() map[string]any {
	return outObject(map[string]any{
		"area":       outString("The feature, as PARITY.md names it (often with the date it shipped)."),
		"hosted":     outString("How simple-host.app and the small box do it."),
		"enterprise": outString("How Simple Host Enterprise does it."),
		"status":     outString("The status cell as written: same, different on purpose (reason), gap → hosted or gap → enterprise."),
		"state":      outEnum("The status reduced to one word.", "same", "different_on_purpose", "gap_hosted", "gap_enterprise", "not_applicable", "other"),
		"date":       outString("The date in the area name, or empty."),
	}, "area", "hosted", "enterprise", "status", "state", "date")
}

func brainFreshnessSchema() map[string]any {
	return outObject(map[string]any{
		"built_at": outString("When the bundle was built (RFC 3339)."),
		"sources": outArray("The repo commits it was built from.", outObject(map[string]any{
			"repo": outString("Repo name."), "commit": outString("Short commit."), "commit_time": outString("Commit time (RFC 3339)."),
			"label": outString("Set for a local source that is a live product or an unshipped plan; empty for the two main repos."),
		}, "repo", "commit", "commit_time", "label")),
		"parity_copies_match": outBool("Whether PARITY.md is identical in both repos, as the rule requires."),
	}, "built_at", "sources", "parity_copies_match")
}

func brainSince(what string) map[string]any {
	return str("Only " + what + " on or after this date: 2026-10-13, or a span back from today such as 3d or 2w.")
}

func brainLimit(def, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": max, "description": "How many to return, default " + strconv.Itoa(def) + ", at most " + strconv.Itoa(max) + "."}
}

// BrainTools is the knowledge server's tool set.
func BrainTools(q BrainQuery) []Tool {
	counts := map[string]any{}
	for _, s := range []string{"same", "different_on_purpose", "gap_hosted", "gap_enterprise", "not_applicable", "other"} {
		counts[s] = outInteger("Rows with this state.")
	}
	return []Tool{
		brainTool("product_overview", "Product overview",
			"What a product is and why it exists, who it is for, what success looks like, its non-goals, its current version and its latest decisions, straight from its INTENT.md. Also says when this knowledge was built and from which commits. product: host (simple-host.app), hack (simple-hack.app), enterprise (Simple Host Enterprise), selfhost-host (the small box anyone runs), selfhost-hack (the Simple Hack standalone package), hack-companies (Simple Hack for companies), or all.",
			object(map[string]any{"product": map[string]any{"type": "string", "enum": brainProducts, "description": "Which product, or all for a short line on each."}}, "product"),
			outObject(map[string]any{
				"products": outArray("One entry per product asked for.", outObject(map[string]any{
					"id": outString("Product id."), "name": outString("Product name."),
					"what":      outString("What it is and why it exists."),
					"who_for":   outString("Who uses it, and in what situation (may be empty)."),
					"success":   outString("What success looks like (may be empty)."),
					"non_goals": outArray("What it deliberately does not do.", outString("One non-goal.")),
					"version":   outString("Current version and what is live."),
					"sources":   outArray("Source document sections.", outString("path#section")),
					"recent_decisions": outArray("Its five newest decisions.", outObject(map[string]any{
						"date": outString("Decision date."), "title": outString("Decision."), "source": outString("path#section"),
					}, "date", "title", "source")),
				}, "id", "name", "what", "who_for", "success", "non_goals", "version", "sources", "recent_decisions")),
				"freshness": brainFreshnessSchema(),
			}, "products", "freshness"), q),

		brainTool("feature_lookup", "Look up a feature",
			"Find features by name or keyword, for example \"WordPress importer\", \"site passcode\" or \"Turnstile comments\". Returns how each one works and where it lives (from both repos' FEATURES.md), which edition it is from, and the matching PARITY.md rows that say which editions have it. Read a whole section with get_doc.",
			object(map[string]any{"query": str("Feature name or keywords."), "limit": brainLimit(5, 15)}, "query"),
			outObject(map[string]any{
				"query": outString("The query."),
				"features": outArray("Best matching FEATURES.md sections first.", outObject(map[string]any{
					"name": outString("Section heading."), "edition": outString("Which product's FEATURES.md it is from."),
					"how_it_works": outString("The part of the section that matched, up to about 1,500 characters."),
					"source":       outString("path#section, for get_doc."),
					"label":        outString("Set for a local source that is a live product or an unshipped plan, never both without saying which; empty for the two main repos."),
				}, "name", "edition", "how_it_works", "source", "label")),
				"parity": outArray("Matching parity rows, best first.", brainParityRowSchema()),
			}, "query", "features", "parity"), q),

		brainTool("parity", "Parity across editions",
			"Which editions have a feature, from PARITY.md: for each row, how hosted (simple-host.app and the small box) and Enterprise do it and its status (same, different on purpose with the reason, gap → hosted, gap → enterprise). With feature, the best matching rows in full; without it, the whole table with shortened cells and counts per status.",
			object(map[string]any{"feature": str("Feature name or keywords. Leave out for the whole table.")}),
			outObject(map[string]any{
				"feature": outString("The feature asked for, or empty for the whole table."),
				"rows":    outArray("Parity rows.", brainParityRowSchema()),
				"counts":  outObject(counts),
				"about": outObject(map[string]any{
					"editions": outString("What hosted and enterprise mean here."), "source": outString("Where the table lives."),
				}, "editions", "source"),
			}, "feature", "rows", "counts", "about"), q),

		brainTool("decisions", "Decisions",
			"The owner's dated decisions and their reasons, from both INTENT.md files, newest first (or best match first with query). For example query \"Turnstile\" or since \"2026-10-01\".",
			object(map[string]any{
				"since": brainSince("decisions"), "query": str("Words to match in the decision."),
				"product": map[string]any{"type": "string", "enum": brainProducts, "description": "Only decisions about this product."},
				"limit":   brainLimit(15, 50),
			}),
			outObject(map[string]any{
				"since": outString("The date applied, or empty."), "query": outString("The words applied, or empty."),
				"total": outInteger("How many decisions matched before the limit."),
				"decisions": outArray("Matching decisions.", outObject(map[string]any{
					"date": outString("Decision date."), "title": outString("The decision in one line."),
					"text": outString("The decision and its reason, up to about 1,800 characters."), "product": outString("Which product it is mostly about."),
					"source": outString("path#section, for get_doc."), "repo": outString("Which repo it is from."),
					"label": outString("Set for a local source that is a live product or an unshipped plan; empty for the two main repos."),
				}, "date", "title", "text", "product", "source", "repo", "label")),
			}, "since", "query", "total", "decisions"), q),

		brainTool("recent_changes", "Recent changes",
			"What changed, from both CHANGELOG.md files, newest first: each dated entry and its lines. since defaults to the last 7 days. Enterprise's Unreleased entry is always included.",
			object(map[string]any{
				"since":   brainSince("changes"),
				"product": map[string]any{"type": "string", "enum": brainProducts, "description": "Only changes about this product."},
				"limit":   brainLimit(30, 100),
			}),
			outObject(map[string]any{
				"since": outString("The date applied."), "product": outString("The product applied, or empty."),
				"changes": outArray("Changelog entries.", outObject(map[string]any{
					"date": outString("Entry date, or unreleased."), "title": outString("Entry heading."),
					"items": outArray("What changed.", outString("One change.")), "repo": outString("Which repo's changelog."),
					"product": outString("Which product it is mostly about."), "source": outString("path#section"),
					"label": outString("Set for a local source that is a live product or an unshipped plan; empty for the two main repos."),
				}, "date", "title", "items", "repo", "product", "source", "label")),
			}, "since", "product", "changes"), q),

		brainTool("search", "Search everything",
			"Full-text search over every source document (INTENT, FEATURES, PARITY, CHANGELOG, llms.txt, design, platform and release docs of both repos). Returns the best matching sections with a snippet and where they are; read one with get_doc.",
			object(map[string]any{"query": str("Words to search for."), "limit": brainLimit(8, 25)}, "query"),
			outObject(map[string]any{
				"query": outString("The query."),
				"results": outArray("Best match first.", outObject(map[string]any{
					"path": outString("Document path."), "section": outString("Section id."), "heading": outString("Section heading."),
					"snippet": outString("Text around the match."), "score": map[string]any{"type": "number", "description": "Relevance."},
					"label": outString("Set for a local source that is a live product or an unshipped plan; empty for the two main repos."),
				}, "path", "section", "heading", "snippet", "score", "label")),
			}, "query", "results"), q),

		brainTool("get_doc", "Read a source document",
			"Read one source document, or one section of it, by the path the other tools return (for example simple-host/INTENT.md, or simple-host/PARITY.md#feature-table). Only the documents this server was built from can be read. A long document comes back shortened with its outline; then ask for a section.",
			object(map[string]any{
				"path":    str("Document path such as simple-host/FEATURES.md; a #section suffix is accepted."),
				"section": str("Section id or heading, optional."),
			}, "path"),
			outObject(map[string]any{
				"path": outString("Document path."), "section": outString("Section asked for, or empty."), "title": outString("Document title."),
				"text": outString("The document or section as Markdown, prefixed with its label when it has one."), "truncated": outBool("Whether the text was shortened."),
				"label": outString("Set for a local source that is a live product or an unshipped plan; empty for the two main repos."),
				"outline": outArray("Every heading in the document.", outObject(map[string]any{
					"id": outString("Section id."), "heading": outString("Heading."), "level": outInteger("Heading level."),
				}, "id", "heading", "level")),
			}, "path", "section", "title", "text", "truncated", "label", "outline"), q),
	}
}

// title is the serverInfo title: the knowledge server names itself.
func (s *Server) title() string {
	if s.fixed != nil && strings.TrimSpace(s.fixed.title) != "" {
		return s.fixed.title
	}
	return "Simple Host"
}
