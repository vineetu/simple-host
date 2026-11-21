package mcp

import (
	"net/http"
	"net/url"
)

// Site editors: a person who may publish, update and roll back one site of
// an account without belonging to it. The account, or a member of the team
// (owner=<team>), manages them. Registered with the team tools.

func editorTools() []Tool {
	editors := outObject(map[string]any{
		"editors": outArray("The site's editors, earliest first. Emails are never shown.", memberSchema()),
	}, "editors")
	return []Tool{
		{
			policy: toolPolicy{route: "GET /v1/sites/{sitename}/editors"}, OutputSchema: editors,
			Name: "list_site_editors", Title: "List a site's editors",
			Description: "List the people who may edit one site without belonging to its account: they publish, update and roll back that site and manage its saved data, and nothing else.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run:         runListSiteEditors,
		},
		{
			policy: toolPolicy{route: "POST /v1/sites/{sitename}/editors"}, OutputSchema: editors,
			Name: "add_site_editor", Title: "Let a person edit one site",
			Description: "Make a person an editor of one site, by their handle: with their own sign-in and owner=<this account> they can publish, update and roll back that one site (creating it if it does not exist yet) and manage its saved data; they cannot touch the account's other sites, its members or the site's address and access. Confirm the person and the site with the person you are helping first.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "person": str("The person's handle (their address).")}, "site", "person"),
			Annotations: writes(false, true, false),
			run:         runAddSiteEditor,
		},
		{
			policy: toolPolicy{route: "DELETE /v1/sites/{sitename}/editors/{person}"}, OutputSchema: editors,
			Name: "remove_site_editor", Title: "Stop a person editing a site",
			Description: "Stop a person editing one site; their next request is refused. Ask first.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "person": str("The editor's handle, from list_site_editors.")}, "site", "person"),
			Annotations: writes(true, true, false),
			run:         runRemoveSiteEditor,
		},
	}
}

func runListSiteEditors(c *call, args map[string]any) (output, error) {
	p, err := editorsPath(args, "")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "list_site_editors", http.MethodGet, p, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func runAddSiteEditor(c *call, args map[string]any) (output, error) {
	p, err := editorsPath(args, "")
	if err != nil {
		return output{}, err
	}
	person, err := stringArg(args, "person")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "add_site_editor", http.MethodPost, p, map[string]string{"person": person})
	if err != nil {
		return output{}, err
	}
	return output{Text: "Editor added. Tell them to publish with owner set to this account's handle.", Structured: out}, nil
}

func runRemoveSiteEditor(c *call, args map[string]any) (output, error) {
	person, err := stringArg(args, "person")
	if err != nil {
		return output{}, err
	}
	p, err := editorsPath(args, "/"+url.PathEscape(person))
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "remove_site_editor", http.MethodDelete, p, nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: "Editor removed.", Structured: out}, nil
}

// editorsPath is the REST path of the editors of the site args names.
func editorsPath(args map[string]any, suffix string) (string, error) {
	site, err := siteArg(args)
	if err != nil {
		return "", err
	}
	return sitePath(site, "/editors"+suffix), nil
}
