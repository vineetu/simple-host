package hackmcp

import (
	"embed"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"regexp"
	"strings"
)

// hackRecipeFiles holds the one recipe that is Simple Hack's own: the
// organiser's event website, in its preset and its rules wording.
//
//go:embed recipes/*.md recipes/rules/*.md
var hackRecipeFiles embed.FS

// readEventRecipe chooses at request time, as the tool server does for its
// own recipes: switching the flag never changes the legacy file.
func readEventRecipe() ([]byte, error) {
	dir := "recipes/"
	if mcp.StorageRulesEnabled() {
		dir += "rules/"
	}
	return hackRecipeFiles.ReadFile(dir + "hack-event.md")
}

// hackRecipeNote opens every recipe on Simple Hack: who "owner" is there and
// which tools stand in for the Simple Host ones (owner decision 2026-10-10).
const hackRecipeNote = `On Simple Hack (read this first; it overrides the Simple Host wording below):
- Team site: run the storage_* setup with the selected team (hack_select_team first). "Owner" is the team: these tools, and a team member signed in on the team site's own address, https://<team>.<event>.simple-hack.app/, with the email they use on Simple Hack (the one this connection signed in with). That member's page gets owner rights for saved data until the team's submission deadline, the same moment the team's key stops changing anything; after it they are an ordinary visitor there.
- Organiser's event website: where the recipe says get_page_recipe, use hack_get_page_recipe; use hack_event_storage_* with {"event": "<event slug>"} instead of storage_* with {"site": ...}, publish with hack_publish_event_website instead of update_site, and use the event slug as the page's site name (window.SH_CONFIG = { site: "<event slug>" }). "Owner" is the event's organisers: these tools, and an organiser signed in on https://<event>.simple-hack.app/ with their Simple Hack email, until the event ends.
- Organiser team rosters: hack_get_teams returns names and member IDs without emails by default. Set include_emails: true only when the organiser asks for team members' email addresses. Use hack_get_people for people management or hack_export_participants for a requested participant export.
- storage_visitor_emails, passcodes and named viewers are not offered on Simple Hack; skip any step that uses them. Each website has one 1,000,000-byte pool for KV, SQLite and files.

`

// eventRecipeSlug is the site-name shape event slugs share (rules/sites.ValidSiteName).
var eventRecipeSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// HackPageRecipe is PageRecipe as Simple Hack serves it.
func HackPageRecipe(topic, site string) (string, error) {
	if topic == "event" {
		if !eventRecipeSlug.MatchString(site) {
			return "", fmt.Errorf("site must be the event slug (lowercase letters, digits and hyphens)")
		}
		data, err := readEventRecipe()
		if err != nil {
			return "", err
		}
		return strings.ReplaceAll(string(data), "{event}", site), nil
	}
	// A portal is a private page per client through named viewers, which
	// Simple Hack does not offer: without them its documents would be public.
	if topic == "portal" {
		return "", fmt.Errorf("topic portal is not available on Simple Hack (it needs named viewers); use another topic")
	}
	// The comments preset needs the spam check, which Simple Hack does not
	// run; people sign in to post there (wall).
	if topic == "comments" || topic == "guestbook" {
		return "", fmt.Errorf("topic %s is not available on Simple Hack (it needs the spam check); use topic wall, where people sign in to post", topic)
	}
	// for, the people pick list, and the canonical-email resolution behind
	// both are Host only (SIM-358 review round 2, item D): named viewers
	// and this email resolution are not a Simple Hack concept, and the
	// REST/MCP paths both already refuse for on Simple Hack regardless.
	if topic == "people" {
		return "", fmt.Errorf("topic people is not available on Simple Hack (for and the people pick list are Host only); use another topic")
	}
	text, err := mcp.PageRecipe(topic, site)
	if err != nil {
		return "", err
	}
	photoAdvice := "For photos, a files resource with the same preset and storage_put_file (under 1,000,000 bytes each; resize first)."
	if mcp.StorageRulesEnabled() {
		photoAdvice = "For photos, use a files resource with preset public and storage_put_file (under 1,000,000 bytes each; resize first). Files keep presets; do not copy the key-value rules onto a files resource."
	}
	text = strings.NewReplacer(
		"Pass returned visitor_id values directly in storage_sql_query or storage_sql_execute params for the same site.\n\n", "",
		"each row's visitor_id in connector results is an opaque reference scoped to this site. Only when the owner asks who sent particular entries, storage_visitor_emails resolves their references to sign-in emails. References change after a server restart, so read the row again if needed.", "each row's visitor_id is the customer's sign-in, and storage_visitor_emails turns it into the email they signed in with.",
		"REST uses its existing visitor IDs, not connector references. ", "",
		"Stop first: if the person is adding their own photos (a menu, listing, portfolio, team), do not use this recipe or build an upload page. Call create_upload_link: it shows a photo picker in the chat (or returns photos_url to give them); then get_site and place the images/ files with update_site (keep_other_files: true).\n\n", "",
		"For the owner's own photos (dishes, products), call create_upload_link: it shows a photo picker in the chat (or returns photos_url to give them) and saves them into images/; then add them to the page with update_site (keep_other_files: true). Never build an upload form for the owner.", photoAdvice,
		" Photos never go into SQLite", " For a gallery only the owner fills, skip the upload form: use preset public on the files resource, upload with storage_put_file, and keep section 2's wall (topic public). Photos never go into SQLite",
		"https://simple-host.app/auth.js", "https://simple-hack.app/auth.js",
		"<site>.<handle>.simple-host.app", "<team>.<event>.simple-hack.app",
		"Simple Host account email", "Simple Hack email",
		"Simple Host account", "Simple Hack account",
		"sign in to Simple Host with", "sign in to Simple Hack with",
		"use the email they already use for Simple Host. Call who_am_i with include_email: true only if they ask which email is connected", "it is the email this connection signed in with; tell the person to use that exact address",
		"(use their usual Simple Host sign-in email; only call who_am_i with include_email: true if they ask which email is connected)", "(the email they use on Simple Hack; tell the person that address)",
	).Replace(text)
	return hackRecipeNote + text, nil
}

// hackPageRecipeTool is get_page_recipe for Simple Hack sites. topic
// people is removed from the enum entirely (not only refused at runtime,
// review round 2 item D): for and the people pick list are Host only
// (named viewers and the canonical-email resolution behind both are not a
// Simple Hack concept), so there is no Simple Hack version of this recipe
// to offer, unlike portal/comments/guestbook below, which stay
// listed and are refused with a reason when picked.
func hackPageRecipeTool() mcp.Tool {
	tool := mcp.PageRecipeTool()
	tool.Description = strings.Replace(tool.Description, "; the owner's own photos go through create_upload_link, no page code", "", 1)
	tool.Description = strings.Replace(tool.Description, "people (one page, two views, for many signed-in visitors the owner also works with directly — a coach and clients, a tutor and students, a trainer and members: the owner picks a person, sees and adds to their rows, assigns from a template, replies by adding a row for them; each visitor sees only their own, marked as the owner's when the owner added it), ", "", 1)
	tool.Description += " Topic event builds the organiser’s event website from the live public event feed. On Simple Hack the storage recipes start with a note on who the owner is there (the team, or the event's organisers) and which tools to use; topic portal is not available there, nor comments or guestbook (no spam check there: use wall, where people sign in to post); topic people does not exist there at all (for and the people pick list are Host only)."
	hackTopics := []string{"event"}
	for _, topic := range mcp.RecipeTopics() {
		if topic != "people" {
			hackTopics = append(hackTopics, topic)
		}
	}
	props := map[string]any{}
	for k, v := range tool.InputSchema["properties"].(map[string]any) {
		props[k] = v
	}
	topicProp := map[string]any{}
	for k, v := range props["topic"].(map[string]any) {
		topicProp[k] = v
	}
	topicProp["enum"] = hackTopics
	topicProp["description"] = "event (the organiser’s live event website), or " + topicProp["description"].(string)
	props["topic"] = topicProp
	schema := map[string]any{}
	for k, v := range tool.InputSchema {
		schema[k] = v
	}
	schema["properties"] = props
	tool.InputSchema = schema
	return mcp.WithRun(tool, func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		topic, err := mcp.StringArg(args, "topic")
		if err != nil {
			return mcp.Output{}, err
		}
		site, err := mcp.OptionalString(args, "site")
		if err != nil {
			return mcp.Output{}, err
		}
		text, err := HackPageRecipe(topic, site)
		if err != nil {
			return mcp.Output{}, err
		}
		if topic == "event" {
			text = hackInstanceReplace(text, c.APIHost())
		}
		return mcp.Output{Text: text, Structured: map[string]any{"topic": topic, "recipe": text}}, nil
	})
}
