#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MCP_SERVER_DIR="$SCRIPT_DIR/mcp-server"
MCP_DIST_DIR="$MCP_SERVER_DIR/dist"
MCP_ENTRYPOINT="$MCP_DIST_DIR/index.js"
SKILL_SOURCE="$SCRIPT_DIR/skills/website-deploy/SKILL.md"

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

ensure_skill_installed() {
  local target="$1"

  ensure_parent_dir "$target"
  if [[ ! -f "$SKILL_SOURCE" ]]; then
    log "Skill source not found at $SKILL_SOURCE; skipping copy to $target"
    return 1
  fi

  cp "$SKILL_SOURCE" "$target"
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
  local tool config_path skill_path
  config_path="$HOME/.claude/settings.json"
  skill_path="$HOME/.claude/skills/website-deploy/SKILL.md"

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
  ensure_skill_installed "$skill_path"
  log "Configured Claude Code."
  add_summary "Claude Code: MCP configured at $config_path and skill installed at $skill_path"
}

install_codex_cli() {
  local config_path skill_path
  config_path="$HOME/.codex/config.toml"
  skill_path="$HOME/.agents/skills/website-deploy/SKILL.md"

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

  ensure_skill_installed "$skill_path"
  log "Configured Codex CLI skill."
  add_summary "Codex CLI: MCP configured at $config_path and skill installed at $skill_path"
}

install_cursor() {
  local tool config_path skill_path
  config_path="$HOME/.cursor/mcp.json"
  skill_path="$HOME/.cursor/skills/website-deploy/SKILL.md"

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
  ensure_skill_installed "$skill_path"
  log "Configured Cursor."
  add_summary "Cursor: MCP configured at $config_path and skill installed at $skill_path"
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
