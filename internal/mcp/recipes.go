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

// One recipe per storage preset, plus the owner's admin page and the files
// gallery. form is the earlier name of inbox.
var recipeTopics = []string{"public", "inbox", "wall", "records", "personal", "board", "private", "admin", "gallery", "form"}

var recipeAliases = map[string]string{"form": "inbox"}

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

// hackRecipeNote opens every recipe on Simple Hack: who "owner" is there and
// which tools stand in for the Simple Host ones (owner decision 2026-10-10).
const hackRecipeNote = `On Simple Hack (read this first; it overrides the Simple Host wording below):
- Team site: run the storage_* setup with the selected team (hack_select_team first). "Owner" is the team: these tools, and a team member signed in on the team site's own address, https://<team>.<event>.simple-hack.app/, with the email they use on Simple Hack (the one this connection signed in with). That member's page gets owner rights for saved data until the team's submission deadline, the same moment the team's key stops changing anything; after it they are an ordinary visitor there.
- Organiser's event website: where the recipe says get_page_recipe, use hack_get_page_recipe; use hack_event_storage_* with {"event": "<event slug>"} instead of storage_* with {"site": ...}, publish with hack_publish_event_website instead of update_site, and use the event slug as the page's site name (window.SH_CONFIG = { site: "<event slug>" }). "Owner" is the event's organisers: these tools, and an organiser signed in on https://<event>.simple-hack.app/ with their Simple Hack email, until the event ends.
- storage_visitor_emails, passcodes and named viewers are not offered on Simple Hack; skip any step that uses them. Each website has one 1,000,000-byte pool for KV, SQLite and files.

`

// HackPageRecipe is PageRecipe as Simple Hack serves it.
func HackPageRecipe(topic, site string) (string, error) {
	text, err := PageRecipe(topic, site)
	if err != nil {
		return "", err
	}
	text = strings.NewReplacer(
		"https://simple-host.app/auth.js", "https://simple-hack.app/auth.js",
		"<site>.<handle>.simple-host.app", "<team>.<event>.simple-hack.app",
		"Simple Host account email", "Simple Hack email",
		"Simple Host account", "Simple Hack account",
		"sign in to Simple Host with", "sign in to Simple Hack with",
		"call who_am_i and tell the person that exact address", "it is the email this connection signed in with; tell the person to use that exact address",
		"(who_am_i shows it; tell the person that address)", "(the email they use on Simple Hack; tell the person that address)",
	).Replace(text)
	return hackRecipeNote + text, nil
}

// hackPageRecipeTool is get_page_recipe for Simple Hack sites.
func hackPageRecipeTool() Tool {
	tool := pageRecipeTool()
	tool.Description += " On Simple Hack the recipe starts with a note on who the owner is there (the team, or the event's organisers) and which tools to use."
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
		Name:        "get_page_recipe",
		Title:       "Page code for each storage preset, sign-in, orders, forms, and uploads",
		Description: "Return the exact owner setup and a complete, working page for a common job, so you do not have to guess the API. One topic per storage preset: public (a menu, prices, or catalogue the owner keeps and everyone reads), inbox (a contact or feedback form only the owner reads; form is the same), wall (a guestbook, comments, or reviews: signed-in people post, everyone reads, authors delete their own), records (each person's records: a shop's cart and orders, bookings, RSVPs; each person sees only their own, the owner sees all and sets a status), personal (a wishlist, notes, or settings each person keeps for themselves), board (a potluck or sign-up sheet everyone signed in edits), private (data only the owner sees). Also admin (an owner page inside the site that lists all orders and sets a status, for the owner signed in on the site with their account email) and gallery (visitors upload photos everyone sees). All use visitor sign-in (SH.mount, an emailed code or Google, on the site's own address) and a storage resource; none uses an API key or account sign-in. Call it before writing any page that signs visitors in, saves what they send, or takes uploads; then run its storage_* setup calls, adapt its page to the person's site, and publish.",
		InputSchema: object(map[string]any{
			"topic": map[string]any{"type": "string", "enum": recipeTopics, "description": "public, inbox, wall, records, personal, board, or private (one per storage preset), admin (the owner's page for all orders), or gallery (photos visitors upload). form is inbox."},
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
			text, err := PageRecipe(topic, site)
			if err != nil {
				return output{}, err
			}
			return output{Text: text, Structured: map[string]any{"topic": topic, "recipe": text}}, nil
		},
	}
}
