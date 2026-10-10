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

var recipeTopics = []string{"records", "form"}

// PageRecipe returns the recipe for topic with the site name filled in.
func PageRecipe(topic, site string) (string, error) {
	data, err := recipeFiles.ReadFile("recipes/" + topic + ".md")
	if err != nil {
		return "", fmt.Errorf("topic must be one of: %s", strings.Join(recipeTopics, ", "))
	}
	if site == "" {
		site = "my-site"
	}
	return addressText(strings.ReplaceAll(string(data), "{site}", site)), nil
}

func pageRecipeTool() Tool {
	return Tool{
		Name:        "get_page_recipe",
		Title:       "Page code for sign-in, orders, and forms",
		Description: "Return the exact owner setup and a complete, working page for a common job, so you do not have to guess the API. Topics: records (each person's records: a shop's cart and orders, RSVPs, bookings, applications, support requests; customers sign in on the site, add their own records, and see only their own, while the owner sees all and sets a status) and form (a contact or feedback form the owner reads). Both use visitor sign-in (SH.mount, an emailed code or Google, on the site's own address) and a SQLite resource; neither uses an API key or account sign-in. Read the recipe, run its storage_* setup calls, then adapt its page to the person's site and publish.",
		InputSchema: object(map[string]any{
			"topic": map[string]any{"type": "string", "enum": recipeTopics, "description": "records (orders, RSVPs, bookings, each person sees only their own) or form (entries only the owner reads)."},
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
