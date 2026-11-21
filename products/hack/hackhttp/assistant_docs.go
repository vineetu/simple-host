package hackhttp

import (
	"io/fs"
	"strings"
)

// AssistantDocsTextCapBytes caps the whole DOCS block the organiser assistant's system prompt embeds (16 KB total).
const AssistantDocsTextCapBytes = 16 * 1024

// AssistantDocPages are the Hack skills the assistant knows about.
var AssistantDocPages = []struct{ Path, URL string }{
	{"skills/run-hackathon/SKILL.md", "/v1/skills/run-hackathon"},
	{"skills/join-hackathon/SKILL.md", "/v1/skills/join-hackathon"},
	{"skills/judge-hackathon/SKILL.md", "/v1/skills/judge-hackathon"},
}

// AssistantDocsText builds the DOCS block (each page as "## [<title>](<url>)"
// plus its extracted text, capped) from fsys.
func AssistantDocsText(fsys fs.FS) string {
	var b strings.Builder
	for _, p := range AssistantDocPages {
		raw, err := fs.ReadFile(fsys, p.Path)
		if err != nil {
			continue
		}
		title, body := extractAssistantSkill(raw)
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

// extractAssistantSkill returns a skill's title (its first "# " heading)
// and its text without the front matter, whitespace-collapsed.
func extractAssistantSkill(raw []byte) (title, body string) {
	text := string(raw)
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if end := strings.Index(rest, "\n---"); end >= 0 {
			text = rest[end+4:]
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if heading, ok := strings.CutPrefix(line, "# "); ok {
			title = strings.TrimSpace(heading)
			break
		}
	}
	return title, strings.Join(strings.Fields(text), " ")
}
