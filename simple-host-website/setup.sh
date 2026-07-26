#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MCP_SERVER_DIR="$SCRIPT_DIR/mcp-server"
MCP_DIST_DIR="$MCP_SERVER_DIR/dist"
MCP_ENTRYPOINT="$MCP_DIST_DIR/index.js"
SKILLS_SOURCE_DIR="$SCRIPT_DIR/skills"

SUMMARY_LINES=()

log() {
  printf '%s\n' "$*"
}

add_summary() {
  SUMMARY_LINES+=("$1")
}

ensure_parent_dir() {
  mkdir -p "$(dirname "$1")"
}

# Install EVERY bundled skill into an agent's skills root, copying each skill's
# whole directory. A skill is no longer one file: website-deploy's SKILL.md
# routes to references/*.md, and copying only SKILL.md would leave the agent
# holding a table of contents with no chapters.
ensure_skills_installed() {
  local skills_root="$1"
  local skill_dir name

  if [[ ! -d "$SKILLS_SOURCE_DIR" ]]; then
    log "Skill source not found at $SKILLS_SOURCE_DIR; skipping copy to $skills_root"
    return 1
  fi

  mkdir -p "$skills_root"
  for skill_dir in "$SKILLS_SOURCE_DIR"/*/; do
    [[ -f "$skill_dir/SKILL.md" ]] || continue
    name="$(basename "$skill_dir")"
    # Overwrite in place rather than deleting first — an installer should never
    # rm -rf a directory under the user's home.
    mkdir -p "$skills_root/$name"
    cp -R "$skill_dir". "$skills_root/$name/"
  done
  return 0
}

json_tool() {
  if command -v python3 >/dev/null 2>&1; then
    printf 'python3\n'
    return 0
  fi

  if command -v jq >/dev/null 2>&1; then
    printf 'jq\n'
    return 0
  fi

  return 1
}

merge_mcp_json() {
  local config_path="$1"
  local tool="$2"
  local tmp_file

  ensure_parent_dir "$config_path"
  tmp_file="$(mktemp)"

  if [[ ! -f "$config_path" || ! -s "$config_path" ]]; then
    printf '{}\n' >"$config_path"
  fi

  if [[ "$tool" == "jq" ]]; then
    jq --arg cmd "node" --arg arg "$MCP_ENTRYPOINT" \
      '.mcpServers = (.mcpServers // {}) | .mcpServers["website-deploy"] = {command: $cmd, args: [$arg]}' \
      "$config_path" >"$tmp_file"
  else
    python3 - "$config_path" "$tmp_file" "$MCP_ENTRYPOINT" <<'PY'
import json
import sys
from pathlib import Path

src = Path(sys.argv[1])
dst = Path(sys.argv[2])
entrypoint = sys.argv[3]

try:
    data = json.loads(src.read_text())
    if not isinstance(data, dict):
        data = {}
except Exception:
    data = {}

mcp_servers = data.get("mcpServers")
if not isinstance(mcp_servers, dict):
    mcp_servers = {}

mcp_servers["website-deploy"] = {
    "command": "node",
    "args": [entrypoint],
}
data["mcpServers"] = mcp_servers
dst.write_text(json.dumps(data, indent=2) + "\n")
PY
  fi

  mv "$tmp_file" "$config_path"
}

ensure_codex_config() {
  local config_path="$1"
  local section='[mcp.website-deploy]'

  ensure_parent_dir "$config_path"
  touch "$config_path"

  if grep -Eq '^\[mcp\.website-deploy\]$' "$config_path"; then
    return 1
  fi

  {
    if [[ -s "$config_path" ]]; then
      printf '\n'
    fi
    printf '%s\n' "$section"
    printf 'command = "node"\n'
    printf 'args = ["%s"]\n' "$MCP_ENTRYPOINT"
  } >>"$config_path"

  return 0
}

ensure_mcp_server_ready() {
  if [[ ! -d "$MCP_SERVER_DIR" ]]; then
    log "Missing mcp-server directory at $MCP_SERVER_DIR"
    exit 1
  fi

  if [[ ! -d "$MCP_SERVER_DIR/node_modules" ]]; then
    log "Installing npm dependencies in $MCP_SERVER_DIR"
    (
      cd "$MCP_SERVER_DIR"
      npm install
    )
  fi

  if [[ ! -d "$MCP_DIST_DIR" || ! -f "$MCP_ENTRYPOINT" ]]; then
    log "Building MCP server in $MCP_SERVER_DIR"
    (
      cd "$MCP_SERVER_DIR"
      npm run build
    )
  fi
}

install_claude_code() {
  local tool config_path skills_root
  config_path="$HOME/.claude/settings.json"
  skills_root="$HOME/.claude/skills"

  if ! command -v claude >/dev/null 2>&1; then
    log "Claude Code not detected; skipping."
    add_summary "Claude Code: skipped (command not found)"
    return 0
  fi

  if ! tool="$(json_tool)"; then
    log "Neither jq nor python3 is available; cannot update Claude Code settings."
    add_summary "Claude Code: skipped (missing jq/python3 for JSON merge)"
    return 0
  fi

  merge_mcp_json "$config_path" "$tool"
  ensure_skills_installed "$skills_root"
  log "Configured Claude Code."
  add_summary "Claude Code: MCP configured at $config_path and skills installed in $skills_root"
}

install_codex_cli() {
  local config_path skills_root
  config_path="$HOME/.codex/config.toml"
  skills_root="$HOME/.agents/skills"

  if ! command -v codex >/dev/null 2>&1; then
    log "Codex CLI not detected; skipping."
    add_summary "Codex CLI: skipped (command not found)"
    return 0
  fi

  if ensure_codex_config "$config_path"; then
    log "Added Codex CLI MCP config."
  else
    log "Codex CLI MCP config already present; leaving it unchanged."
  fi

  ensure_skills_installed "$skills_root"
  log "Configured Codex CLI skill."
  add_summary "Codex CLI: MCP configured at $config_path and skills installed in $skills_root"
}

install_cursor() {
  local tool config_path skills_root
  config_path="$HOME/.cursor/mcp.json"
  skills_root="$HOME/.cursor/skills"

  if [[ ! -d "$HOME/.cursor" ]]; then
    log "Cursor not detected; skipping."
    add_summary "Cursor: skipped (~/.cursor not found)"
    return 0
  fi

  if ! tool="$(json_tool)"; then
    log "Neither jq nor python3 is available; cannot update Cursor MCP config."
    add_summary "Cursor: skipped (missing jq/python3 for JSON merge)"
    return 0
  fi

  merge_mcp_json "$config_path" "$tool"
  ensure_skills_installed "$skills_root"
  log "Configured Cursor."
  add_summary "Cursor: MCP configured at $config_path and skills installed in $skills_root"
}

main() {
  log "Website Deploy setup starting from $SCRIPT_DIR"
  ensure_mcp_server_ready
  install_claude_code
  install_codex_cli
  install_cursor

  log ""
  log "Summary:"
  for line in "${SUMMARY_LINES[@]}"; do
    printf ' - %s\n' "$line"
  done
}

main "$@"
