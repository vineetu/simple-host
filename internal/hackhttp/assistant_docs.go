package hackhttp

import (
	"bytes"
	"io/fs"
	"strings"

	"golang.org/x/net/html"
)

// AssistantDocsTextCapBytes caps the whole DOCS block the SIM-394 organiser
// assistant's system prompt embeds (api-contract.md: "cap 16 KB total").
const AssistantDocsTextCapBytes = 16 * 1024

// AssistantDocPages are the Hack docs pages the assistant knows about
// (api-contract.md). Path is relative to the docs filesystem passed to
// AssistantDocsText (webassets.DocsTree(true) in production); URL is the
// public /docs/... address included in the prompt's DOCS section.
var AssistantDocPages = []struct{ Path, URL string }{
	{"start/index.html", "/docs/start/"},
	{"start/judge/index.html", "/docs/start/judge/"},
	{"start/join/index.html", "/docs/start/join/"},
	{"limits/index.html", "/docs/limits/"},
}

// AssistantDocsText builds the DOCS block (each page as
// "## [<title>](<url>)" plus its extracted text, capped) from fsys. It is
// the one place this HTML-to-text extraction lives: cmd/server calls it to
// build the real server's prompt (internal/app/hack/events may not import
// internal/webassets -- scripts/layering/rules.txt -- so cmd/server passes
// webassets.DocsTree(true) in), and internal/hackhttp's own eval test
// (assistant_eval_test.go) calls it too, so the eval runs against the
// exact production prompt instead of a second, maybe-drifted copy of this
// extraction.
func AssistantDocsText(fsys fs.FS) string {
	var b strings.Builder
	for _, p := range AssistantDocPages {
		raw, err := fs.ReadFile(fsys, p.Path)
		if err != nil {
			continue
		}
		title, body := extractAssistantDocPage(raw)
		if title == "" {
			title = p.URL
		}
		b.WriteString("## [")
		b.WriteString(title)
		b.WriteString("](")
		b.WriteString(p.URL)
		b.WriteString(")\n")
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	text := b.String()
	if len(text) > AssistantDocsTextCapBytes {
		text = text[:AssistantDocsTextCapBytes]
	}
	return text
}

// extractAssistantDocPage returns a docs page's title (<h1>, else <title>)
// and its <main> content as plain, whitespace-collapsed text: every tag,
// script and style stripped (api-contract.md).
func extractAssistantDocPage(raw []byte) (title, body string) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	var mainNode, h1Node, titleNode *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "main":
				if mainNode == nil {
					mainNode = n
				}
			case "h1":
				if h1Node == nil {
					h1Node = n
				}
			case "title":
				if titleNode == nil {
					titleNode = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var textOf func(*html.Node) string
	textOf = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		if n.Type != html.ElementNode || n.Data == "script" || n.Data == "style" {
			return ""
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(textOf(c))
			b.WriteString(" ")
		}
		return b.String()
	}

	if h1Node != nil {
		title = collapseAssistantWhitespace(textOf(h1Node))
	} else if titleNode != nil {
		title = collapseAssistantWhitespace(textOf(titleNode))
	}
	if mainNode != nil {
		body = collapseAssistantWhitespace(textOf(mainNode))
	}
	return title, body
}

func collapseAssistantWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
