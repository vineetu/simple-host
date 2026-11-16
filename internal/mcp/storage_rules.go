package mcp

import (
	"os"
	"strings"
)

func storageRulesEnabled() bool { return os.Getenv("STORAGE_RULES") == "on" }

const storageRulesInstructions = `SQLite/KV: ask who may read, add, edit and delete, then write rules. Deny by default: require a permissive policy and every restrictive policy. Owner tools bypass rules; owner pages need auth.role == "owner". Files keep preset fields. Read storage_list_resources before changes; ask before widening access.`

const storageRulesSetDescription = `Create a saved-data resource or set who may read, add, edit and delete. kind: kv (one JSON value per key), sqlite (tables), or files (uploads); it cannot change later.

For SQLite and SQLite-stored key-value data, ask who should read and write which data, then supply rules: {version:1, policies:[...]}. Deny by default: without a permissive policy for the command, visitors cannot run it; an empty policies array denies every visitor. Permissive policies combine with OR; restrictive policies combine with AND and cannot grant access alone. Table rules replace the resource rules for that table; tables without an override inherit the resource rules. Send tables:{} when replacing existing table settings with one resource rule set. Do not combine rules with preset, read, add, edit, delete, write, write_mode or approve_first.

Clause table (command | existing row using | new row check):
read | required | refused
add | refused | required
edit | required | optional, defaults to using
delete | required | refused
all | required for read/edit/delete | optional for add/edit, defaults to using
A command without a matching permissive policy is denied regardless of these clause defaults. Edit and delete also require read visibility. Anonymous edits and deletes are refused. Use fixed page methods SH.storage.sqlite(name).table(t).list/get/add/edit/delete; visitors never send SQL. Bindings are trusted server facts: auth.id, auth.signed_in, auth.role; visitor_id is the row's author. KV rules use key, value and visitor_id; value is the whole JSON text, with no nested JSON access. Owner tools bypass rules; the owner signed in on the site's own address does not: include auth.role == "owner" when that page should act as an admin page.

Three example rule sets:
{"version":1,"policies":[{"name":"read","command":"read","using":"true"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}
{"version":1,"policies":[{"name":"send","command":"add","check":"true"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}
{"version":1,"policies":[{"name":"read","command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"manage","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}

For kind: "files", keep the preset fields: preset, read, add, edit, delete, site_passcode. File presets remain public, inbox, wall, records, personal, board, private and custom; own means the signed-in uploader, owner means the site owner, and nobody allows no page access. Single-action overrides keep their existing restrictions. Files do not accept rules.

For every resource kind, site_passcode applies alongside rules or presets: inherit requires the site's passcode; off exempts this resource. Omission defaults to inherit, so preserve an existing off exemption when saving.

Create the resource with storage_set_resource, then create its tables with storage_sql_schema only for SQLite, before publishing the page. Ask before widening access.`

const storageRulesListDescription = `List a site's saved-data resources with their names and kinds. SQLite and key-value resources include rules_status (rules, preset or invalid). Resources with rules return rules, rules_errors when invalid, and per-table rules only for overrides. Missing table overrides inherit the resource rules; an empty policy set denies visitors. Unconverted resources (rules_status: "preset") retain their existing preset and access fields. Read this before changing saved data or access.

File buckets keep preset and access (read, add, edit, delete) fields. Uploads are listed by storage_list_file_objects (through run_tool if absent from your tool list).`

const storageRulesQueryDescription = `Read rows from a SQLite resource with a SELECT as the owner: owner tools see every person's rows and bypass row rules. Read-only; put values in params with ? placeholders; on Simple Host, visitor_id values from results can be passed back as parameters. Use it to show the person their orders, entries, or messages. ` + sqlNoPragmaHint

const storageRulesSchemaDescription = `Create or change tables in a SQLite resource: CREATE TABLE, CREATE INDEX, CREATE TRIGGER, ALTER TABLE, one statement per call. Use IF NOT EXISTS for supported CREATE statements. Check the current schema with ` + sqlSchemaIntrospectionHint + ` (no PRAGMA) before repeating ALTER TABLE. Use id INTEGER PRIMARY KEY and created_at TEXT (and updated_at TEXT where rows are edited); the server fills them. Do not declare visitor_id: the server ensures the indexed author column when rules reference visitor_id or allow ordinary visitors to add. If rules name approved_at, the server ensures that column for moderation. A table override replaces the resource rule set; install rules for a new table name before renaming a table with its own rules. A foreign key with ON DELETE CASCADE stops pages deleting the parent row, since that would change another table; leave cascades out of tables pages delete from. Ask before a change that drops or reshapes data.`

// Apply wording after choosing a connection's inventory, so a running server
// never keeps startup-time flag text in its cached declarations or search results.
func (s *Server) storageRulesTool(tool Tool, caller Caller) Tool {
	name := strings.TrimPrefix(tool.Name, "hack_event_")
	switch name {
	case "storage_set_resource":
		tool.Title = "Create a storage resource or set its rules"
		tool.Description = storageRulesSetDescription
		if !s.cfg.NoSpamCheck && caller.Mode != CallerModeEvents && caller.Mode != CallerModeGroupSite && !strings.HasPrefix(tool.Name, "hack_event_") {
			tool.Description += ` For spam-checked adds, use auth.checked in an add check. To hold posts, add a restrictive read policy using auth.role == "owner" || approved_at is not null; approve them by setting approved_at as the owner.`
		}
	case "storage_list_resources":
		tool.Description = storageRulesListDescription
	case "storage_sql_query":
		tool.Description = storageRulesQueryDescription
	case "storage_sql_schema":
		tool.Description = storageRulesSchemaDescription
	default:
		return tool
	}
	if strings.HasPrefix(tool.Name, "hack_event_") {
		if name == "storage_set_resource" {
			tool.Title = "Event website: " + tool.Title
		}
		tool.Description = strings.ReplaceAll(tool.Description, "storage_", "hack_event_storage_")
		tool.Description += ` Owner on this website means a current event organiser until the event ends. Organiser only on this event's custom website.`
	} else if caller.Mode == CallerModeGroupSite || caller.Mode == CallerModeEvents {
		tool.Description += ` Owner on a team site means a current approved teammate until the team's submission deadline, subject to stage and take-down checks; after that the teammate is an ordinary visitor.`
	}
	if caller.Mode == CallerModeGroupSite || caller.Mode == CallerModeEvents || strings.HasPrefix(tool.Name, "hack_event_") {
		tool.Description = s.productInstructions(tool.Description)
	} else {
		tool.Description = instanceText(addressText(tool.Description))
	}
	return tool
}

func (s *Server) storageRulesInventory(full, bare []Tool, byName map[string]Tool, c Caller) ([]Tool, []Tool, map[string]Tool) {
	rewrite := func(list []Tool) []Tool {
		out := append([]Tool(nil), list...)
		for i := range out {
			out[i] = s.storageRulesTool(out[i], c)
		}
		return out
	}
	copied := make(map[string]Tool, len(byName))
	for name, tool := range byName {
		copied[name] = s.storageRulesTool(tool, c)
	}
	return rewrite(full), rewrite(bare), copied
}
