package mcp

// toolFamily names one capability family of MCP tools (docs/architecture/
// target.md §6). Every registered tool belongs to exactly one family.
type toolFamily string

const (
	famSites         toolFamily = "sites"         // sites, deploys, versions
	famAccess        toolFamily = "access"        // passcodes, viewers, domains
	famStorage       toolFamily = "storage"       // current storage (KV, SQLite, files) and visitor tools
	famLegacy        toolFamily = "legacy"        // legacy Host storage
	famAccounts      toolFamily = "accounts"      // who am I, profile, connections
	famImports       toolFamily = "imports"       // imports and photo uploads
	famNotifications toolFamily = "notifications" // notifications and MCP Events
	famAdmin         toolFamily = "admin"         // admin and brain
	famDiscovery     toolFamily = "discovery"     // find_tools, run_tool
	famHackEvents    toolFamily = "hack-events"   // Hack events, members, teams
	famHackEntries   toolFamily = "hack-entries"  // Hack entries, judging, results
	famHackAppOnly   toolFamily = "hack-app-only" // Hack app-only screen tools
)

// familyTools is one family's tool declarations, in listing order.
type familyTools struct {
	family toolFamily
	tools  []Tool
}
