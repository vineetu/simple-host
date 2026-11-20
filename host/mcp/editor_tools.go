package mcp

import (
	"net/http"
	"net/url"
)

// Site editors: a person who may publish, update and roll back one site of
// an account without belonging to it. The account, or a member of the team
// (owner=<team>), manages them. Registered with the team tools.

func editorTools() []Tool {
	policy := toolPolicy{groupSiteExcluded: true, notDeployOnly: true}
	editors := outObject(map[string]any{
		"editors": outArray("The site's editors, earliest first. Emails are never shown.", memberSchema()),
	}, "editors")
	path := func(c *call, args map[string]any, suffix string) (string, error) {
		site, err := siteArg(args)
		if err != nil {
			return "", err
		}
		return "/v1/sites/" + url.PathEscape(site) + "/editors" + suffix, nil
	}
	return []Tool{
		{
			policy: policy, OutputSchema: editors,
			Name: "list_site_editors", Title: "List a site's editors",
			Description: "List the people who may edit one site without belonging to its account: they publish, update and roll back that site and manage its saved data, and nothing else.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				p, err := path(c, args, "")
				if err != nil {
					return output{}, err
				}
				out, err := teamCall(c, "list_site_editors", http.MethodGet, p, nil)
				if err != nil {
					return output{}, err
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			policy: policy, OutputSchema: editors,
			Name: "add_site_editor", Title: "Let a person edit one site",
			Description: "Make a person an editor of one site, by their handle: with their own sign-in and owner=<this account> they can publish, update and roll back that one site (creating it if it does not exist yet) and manage its saved data; they cannot touch the account's other sites, its members or the site's address and access. Confirm the person and the site with the person you are helping first.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "person": str("The person's handle (their address).")}, "site", "person"),
			Annotations: writes(false, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				p, err := path(c, args, "")
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
			},
		},
		{
			policy: policy, OutputSchema: editors,
			Name: "remove_site_editor", Title: "Stop a person editing a site",
			Description: "Stop a person editing one site; their next request is refused. Ask first.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "person": str("The editor's handle, from list_site_editors.")}, "site", "person"),
			Annotations: writes(true, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				person, err := stringArg(args, "person")
				if err != nil {
					return output{}, err
				}
				p, err := path(c, args, "/"+url.PathEscape(person))
				if err != nil {
					return output{}, err
				}
				out, err := teamCall(c, "remove_site_editor", http.MethodDelete, p, nil)
				if err != nil {
					return output{}, err
				}
				return output{Text: "Editor removed.", Structured: out}, nil
			},
		},
	}
}
