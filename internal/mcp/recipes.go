package mcp

import (
	"embed"
	"fmt"
	"strings"
)

// Page recipes are the connector's answer to "how does a page do X": exact
// owner setup (which storage_* calls) and a complete page that works on the
// site's own address with visitor sign-in and no API key. They exist so a chat
// with the connector alone, and no skill files, builds the right thing.
//
//go:embed recipes/*.md
var recipeFiles embed.FS

// One recipe per everyday job first (booking, inbox, public, portal), then one
// per remaining storage preset, the owner's admin page, and the files gallery.
// The aliases are the plain words people use for the same jobs; form is the
// earlier name of inbox.
var recipeTopics = []string{"booking", "inbox", "public", "portal", "wall", "comments", "board", "personal", "private", "records", "admin", "gallery", "wordpress",
	"reservation", "appointment", "quote", "waitlist", "contact", "form", "menu", "guestbook"}

var recipeAliases = map[string]string{"form": "inbox", "quote": "inbox", "waitlist": "inbox", "contact": "inbox",
	"reservation": "booking", "appointment": "booking", "menu": "public", "guestbook": "comments"}

// PageRecipe returns the recipe for topic with the site name filled in.
func PageRecipe(topic, site string) (string, error) {
	file := topic
	if alias, ok := recipeAliases[topic]; ok {
		file = alias
	}
	data, err := recipeFiles.ReadFile("recipes/" + file + ".md")
	if err != nil {
		return "", fmt.Errorf("topic must be one of: %s", strings.Join(recipeTopics, ", "))
	}
	if site == "" {
		site = "my-site"
	}
	return addressText(strings.ReplaceAll(string(data), "{site}", site)), nil
}

// hostPageRecipe is PageRecipe for a Simple Host server, which names itself.
func hostPageRecipe(topic, site string) (string, error) {
	text, err := PageRecipe(topic, site)
	if err != nil {
		return "", err
	}
	return instanceText(text), nil
}

// hackRecipeNote opens every recipe on Simple Hack: who "owner" is there and
// which tools stand in for the Simple Host ones (owner decision 2026-10-10).
const hackRecipeNote = `On Simple Hack (read this first; it overrides the Simple Host wording below):
- Team site: run the storage_* setup with the selected team (hack_select_team first). "Owner" is the team: these tools, and a team member signed in on the team site's own address, https://<team>.<event>.simple-hack.app/, with the email they use on Simple Hack (the one this connection signed in with). That member's page gets owner rights for saved data until the team's submission deadline, the same moment the team's key stops changing anything; after it they are an ordinary visitor there.
- Organiser's event website: where the recipe says get_page_recipe, use hack_get_page_recipe; use hack_event_storage_* with {"event": "<event slug>"} instead of storage_* with {"site": ...}, publish with hack_publish_event_website instead of update_site, and use the event slug as the page's site name (window.SH_CONFIG = { site: "<event slug>" }). "Owner" is the event's organisers: these tools, and an organiser signed in on https://<event>.simple-hack.app/ with their Simple Hack email, until the event ends.
- Organiser team rosters: hack_get_teams returns names and member IDs without emails by default. Set include_emails: true only when the organiser asks for team members' email addresses. Use hack_get_people for people management or hack_export_participants for a requested participant export.
- storage_visitor_emails, passcodes and named viewers are not offered on Simple Hack; skip any step that uses them. Each website has one 1,000,000-byte pool for KV, SQLite and files.

`

// HackPageRecipe is PageRecipe as Simple Hack serves it.
func HackPageRecipe(topic, site string) (string, error) {
	// A portal is a private page per client through named viewers, which
	// Simple Hack does not offer: without them its documents would be public.
	if topic == "portal" {
		return "", fmt.Errorf("topic portal is not available on Simple Hack (it needs named viewers); use another topic")
	}
	// The comments preset needs the spam check, which Simple Hack does not
	// run; people sign in to post there (wall).
	// Moving a WordPress site uses create_import_link, which Simple Hack
	// does not offer: team sites are new builds.
	if topic == "wordpress" {
		return "", fmt.Errorf("topic wordpress is not available on Simple Hack (team sites are built new there); use another topic")
	}
	if topic == "comments" || topic == "guestbook" {
		return "", fmt.Errorf("topic %s is not available on Simple Hack (it needs the spam check); use topic wall, where people sign in to post", topic)
	}
	text, err := PageRecipe(topic, site)
	if err != nil {
		return "", err
	}
	text = strings.NewReplacer(
		"Pass returned visitor_id values directly in storage_sql_query or storage_sql_execute params for the same site.\n\n", "",
		"each row's visitor_id in connector results is an opaque reference scoped to this site. Only when the owner asks who sent particular entries, storage_visitor_emails resolves their references to sign-in emails. References change after a server restart, so read the row again if needed.", "each row's visitor_id is the customer's sign-in, and storage_visitor_emails turns it into the email they signed in with.",
		"REST uses its existing visitor IDs, not connector references. ", "",
		"Stop first: if the person is adding their own photos (a menu, listing, portfolio, team), do not use this recipe or build an upload page. Call create_upload_link: it shows a photo picker in the chat (or returns photos_url to give them); then get_site and place the images/ files with update_site (keep_other_files: true).\n\n", "",
		"For the owner's own photos (dishes, products), call create_upload_link: it shows a photo picker in the chat (or returns photos_url to give them) and saves them into images/; then add them to the page with update_site (keep_other_files: true). Never build an upload form for the owner.", "For photos, a files resource with the same preset and storage_put_file (under 1,000,000 bytes each; resize first).",
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

// hackPageRecipeTool is get_page_recipe for Simple Hack sites.
func hackPageRecipeTool() Tool {
	tool := pageRecipeTool()
	tool.Description = strings.Replace(tool.Description, "; the owner's own photos go through create_upload_link, no page code", "", 1)
	tool.Description += " On Simple Hack the recipe starts with a note on who the owner is there (the team, or the event's organisers) and which tools to use; topic portal is not available there, nor comments or guestbook (no spam check there: use wall, where people sign in to post)."
	tool.run = func(c *call, args map[string]any) (output, error) {
		topic, err := stringArg(args, "topic")
		if err != nil {
			return output{}, err
		}
		site, err := optionalString(args, "site")
		if err != nil {
			return output{}, err
		}
		text, err := HackPageRecipe(topic, site)
		if err != nil {
			return output{}, err
		}
		return output{Text: text, Structured: map[string]any{"topic": topic, "recipe": text}}, nil
	}
	return tool
}

func pageRecipeTool() Tool {
	return Tool{
		Name:  "get_page_recipe",
		Title: "Page code for bookings, forms, menus, client portals, and visitor uploads",
		Description: "Return the exact owner setup and a complete, working page for a common job, so you do not have to guess the API. Pick the topic from what the person asked for: " +
			"booking (any booking, appointment, or table reservation, even a \"booking request\": the customer signs in with their email when they book and sees their bookings and status; the owner confirms from chat), " +
			"inbox (a quote request, contact form, waitlist, sign-up, volunteer or job application form, feedback: anyone sends without signing in, only the owner reads), " +
			"public (a menu, prices, opening hours, or stock list the owner changes from chat and everyone reads), " +
			"portal (a client portal or client documents: one private page per client, opened only to that client's email), " +
			"comments (a guestbook, comments, reviews, or questions anyone posts without signing in, after a spam check; the owner removes or approves posts), " +
			"wall (reviews, comments, community posts everyone reads; people sign in to post, and can take back their own), board (a shared list people sign in to edit together: shifts, tasks, a sign-up sheet), " +
			"personal (things each visitor keeps and changes for themselves: favourites, measurements, a checklist, tracking what they applied for), private (the owner's own notes or log on the site), " +
			"records (a shop with a cart and orders), admin (an owner page on the site that lists all orders or bookings and sets a status), gallery (photos visitors upload that everyone sees; the owner's own photos go through create_upload_link, no page code), " +
			"wordpress (moving the person's WordPress site here from their own export: the import link, the export clicks, and which Simple Host feature replaces each WordPress feature). " +
			"Visitors never make an account or a password: visitor sign-in is an emailed code or Google, and only where the job needs it. " +
			"Call it before writing any page that saves what visitors send, shows saved data, or takes visitors' uploads; then run its setup calls, adapt its page to the person's site, and publish.",
		InputSchema: object(map[string]any{
			"topic": map[string]any{"type": "string", "enum": recipeTopics, "description": "booking, inbox, public, portal, wall, comments, board, personal, private, records, admin, gallery, or wordpress. reservation and appointment are booking; quote, waitlist, contact, and form are inbox; menu is public; guestbook is comments."},
			"site":  str("The site's name, so the page code comes back filled in. Optional before the site exists."),
		}, "topic"),
		OutputSchema: outObject(map[string]any{
			"topic":  outString("The recipe returned."),
			"recipe": outString("Owner setup (storage_* calls), the complete page, the owner's view, and how to verify."),
		}, "topic", "recipe"),
		Annotations: readOnly(),
		run: func(c *call, args map[string]any) (output, error) {
			topic, err := stringArg(args, "topic")
			if err != nil {
				return output{}, err
			}
			site, err := optionalString(args, "site")
			if err != nil {
				return output{}, err
			}
			if site != "" {
				if site, err = siteArg(args); err != nil {
					return output{}, err
				}
			}
			text, err := hostPageRecipe(topic, site)
			if err != nil {
				return output{}, err
			}
			return output{Text: text, Structured: map[string]any{"topic": topic, "recipe": text}}, nil
		},
	}
}

// noSpamCheckRecipeTool is get_page_recipe on a server without the spam
// check (no Turnstile keys, as a self-hosted box starts): comments and
// guestbook point to wall, where people sign in to post.
func noSpamCheckRecipeTool(tool Tool) Tool {
	tool.Description = strings.Replace(tool.Description, "comments (a guestbook, comments, reviews, or questions anyone posts without signing in, after a spam check; the owner removes or approves posts), ", "", 1)
	tool.Description = strings.Replace(tool.Description, "wall (reviews, comments, community posts everyone reads;", "wall (a guestbook, reviews, comments, community posts everyone reads;", 1)
	tool.Description += " This server has no spam check, so comments and guestbook are not available: use wall."
	run := tool.run
	tool.run = func(c *call, args map[string]any) (output, error) {
		if topic, _ := args["topic"].(string); topic == "comments" || topic == "guestbook" {
			return output{}, fmt.Errorf("topic %s is not available on this server (it needs the spam check, which needs TURNSTILE_SITE_KEY and a secret); use topic wall, where people sign in to post", topic)
		}
		return run(c, args)
	}
	return tool
}
