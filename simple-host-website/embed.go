// Package plugin embeds the skill bundle so the simple_host server can:
//
//   - Serve /skills.zip (a flat zip of skill folders) for manual install
//     into ~/.claude/skills or ~/.agents/skills (Codex CLI, etc.).
//   - Serve /plugin.zip (the wrapped plugin layout, including
//     .claude-plugin/plugin.json) for `claude --plugin-url <url>`.
//   - Serve /install.sh, which downloads /skills.zip and extracts it.
//
// All install paths are pure HTTPS — no git, no npm, no registry.
package plugin

import "embed"

//go:embed all:skills all:.claude-plugin
var FS embed.FS
