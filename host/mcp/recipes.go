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
// per remaining kind of saved data, the owner's admin page, the files gallery,
// and the owner/visitor page on one site (people). The aliases are the plain
// words people use for the same jobs; form is the earlier name of inbox.
var recipeTopics = []string{"booking", "inbox", "public", "portal", "wall", "comments", "board", "personal", "private", "records", "admin", "people", "gallery",
	"reservation", "appointment", "quote", "waitlist", "contact", "form", "menu", "guestbook"}

var recipeAliases = map[string]string{"form": "inbox", "quote": "inbox", "waitlist": "inbox", "contact": "inbox",
	"reservation": "booking", "appointment": "booking", "menu": "public", "guestbook": "comments"}

// PageRecipe returns the recipe for topic with the site name filled in.
func PageRecipe(topic, site string) (string, error) {
	file := topic
	if alias, ok := recipeAliases[topic]; ok {
		file = alias
	}
	if strings.Contains(file, "/") {
		return "", fmt.Errorf("topic must be one of: %s", strings.Join(recipeTopics, ", "))
	}
	data, err := readRecipe(file)
	if err != nil {
		return "", fmt.Errorf("topic must be one of: %s", strings.Join(recipeTopics, ", "))
	}
	if site == "" {
		site = "my-site"
	}
	return addressText(strings.ReplaceAll(string(data), "{site}", site)), nil
}

func readRecipe(topic string) ([]byte, error) {
	return recipeFiles.ReadFile("recipes/" + topic + ".md")
}

// hostPageRecipe is PageRecipe for a Simple Host server, which names itself.
func hostPageRecipe(topic, site string) (string, error) {
	text, err := PageRecipe(topic, site)
	if err != nil {
		return "", err
	}
	return instanceText(text), nil
}

func pageRecipeTool() Tool {
	return Tool{
		policy: toolPolicy{flatList: flatRecipeTool, personalExcluded: true,
			noSpamCheck: noSpamCheckRecipeTool,
		},
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
			"records (a shop with a cart and orders), admin (an owner page on the site that lists all orders or bookings and sets a status), " +
			"people (one page, two views, for many signed-in visitors the owner also works with directly — a coach and clients, a tutor and students, a trainer and members: the owner picks a person, sees and adds to their rows, assigns from a template, replies by adding a row for them; each visitor sees only their own, marked as the owner's when the owner added it), " +
			"gallery (photos visitors upload that everyone sees; the owner's own photos go through create_upload_link, no page code). " +
			"Visitors never make an account or a password: visitor sign-in is an emailed code or Google, and only where the job needs it. " +
			"Call it before writing any page that saves what visitors send, shows saved data, or takes visitors' uploads; then run its setup calls, adapt its page to the person's site, and publish.",
		InputSchema: object(map[string]any{
			"topic": map[string]any{"type": "string", "enum": recipeTopics, "description": "booking, inbox, public, portal, wall, comments, board, personal, private, records, admin, people, or gallery. reservation and appointment are booking; quote, waitlist, contact, and form are inbox; menu is public; guestbook is comments."},
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
