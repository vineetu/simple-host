package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Who can open a site: named viewers (handler/viewers.go). The names follow
// Simple Host Enterprise (set_site_access, list_site_viewers,
// grant_site_viewer, revoke_site_viewer); hosted names people by the email
// they sign in with.

const accessToolsWhen = "Use this when the person wants only certain people to open a site (\"only family\", \"only mom and dad\", \"just my team\"): each named person signs in on the site with their email (an emailed code or Google) and nobody else gets in. " +
	"It is not set_site_passcode (one shared code anyone can pass on), not set_visibility (listing only; an unlisted site is still open to anyone with the link) and not set_site_offline (closed to everyone). A site has named viewers or a passcode, never both."

// siteAccessResult is the REST answer of every access route.
type siteAccessResult struct {
	Site    string `json:"site"`
	Access  string `json:"access"`
	Viewers []struct {
		Email   string `json:"email"`
		AddedAt string `json:"added_at"`
	} `json:"viewers"`
	ViewerLimit       int      `json:"viewer_limit"`
	PasscodeProtected bool     `json:"passcode_protected"`
	Added             []string `json:"added"`
	AlreadyListed     []string `json:"already_listed"`
	Removed           string   `json:"removed"`
	Note              string   `json:"note"`
}

func siteAccessOutput(tool string, res upstreamResult) (output, error) {
	if !res.ok() {
		return output{}, restError(tool, res)
	}
	var a siteAccessResult
	if err := json.Unmarshal(res.body, &a); err != nil {
		return output{}, errors.New(tool + ": server returned invalid JSON")
	}
	viewers := make([]any, 0, len(a.Viewers))
	emails := make([]string, 0, len(a.Viewers))
	for _, v := range a.Viewers {
		viewers = append(viewers, map[string]any{"email": v.Email, "added_at": v.AddedAt})
		emails = append(emails, v.Email)
	}
	out := map[string]any{"site": a.Site, "access": a.Access, "viewers": viewers, "viewer_limit": a.ViewerLimit, "passcode_protected": a.PasscodeProtected}
	if len(a.Added) > 0 {
		out["added"] = a.Added
	}
	if len(a.AlreadyListed) > 0 {
		out["already_listed"] = a.AlreadyListed
	}
	if a.Removed != "" {
		out["removed"] = a.Removed
	}
	if a.Note != "" {
		out["note"] = a.Note
	}
	var text string
	switch {
	case a.Access == "specific" && len(emails) == 0:
		text = a.Site + " is open only to its owner: no named viewers yet."
	case a.Access == "specific":
		text = a.Site + " is open only to its owner and these named viewers: " + strings.Join(emails, ", ") + "."
	case a.PasscodeProtected:
		text = a.Site + " is open to anyone who has its passcode."
	default:
		text = a.Site + " is open to anyone with its address."
	}
	if a.Note != "" {
		text += " " + a.Note
	}
	return output{Text: text, Structured: out}, nil
}

func siteAccessSchema(optional ...string) map[string]any {
	props := map[string]any{
		"site":   outString(outSiteName),
		"access": outEnum("Who can open the site: anyone (anyone with its address, or with its passcode when it has one) or specific (only the owner and the named viewers, signed in on the site).", "anyone", "specific"),
		"viewers": outArray("The named viewers, oldest first. They matter only while access is specific.", outObject(map[string]any{
			"email":    outString("The email the person signs in with."),
			"added_at": outString("When they were added (RFC 3339)."),
		}, "email", "added_at")),
		"viewer_limit":       outInteger("The most named viewers a site can have on this server."),
		"passcode_protected": outBool("Whether the site has a passcode instead."),
	}
	all := map[string]any{
		"added":          outArray("Emails this call added.", outString("An email.")),
		"already_listed": outArray("Emails this call named that were already on the list.", outString("An email.")),
		"removed":        outString("The email this call removed."),
		"note":           outString("A plain note from Simple Host about the change (pass it on to the person)."),
	}
	for _, k := range optional {
		props[k] = all[k]
	}
	return outObject(props, "site", "access", "viewers", "viewer_limit", "passcode_protected")
}

func accessTools() []Tool {
	path := func(name string) string { return "/v1/sites/" + url.PathEscape(name) }
	return []Tool{
		{
			Name:  "set_site_access",
			Title: "Choose who can open a site",
			Description: "Set who can open a whole site. access `specific`: only the owner and the named viewers (grant_site_viewer adds them, and also turns this on); everyone else sees a sign-in page, and a signed-in person who is not named sees \"This site is private\". access `anyone`: open to anyone with the address again (the named viewers are kept for next time). " +
				accessToolsWhen + " ASK THE PERSON FIRST, naming the site and what changes. The owner's key and this connector keep working either way.",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc),
				"access": map[string]any{"type": "string", "enum": []string{"anyone", "specific"}, "description": "`specific`: only the owner and the named viewers. `anyone`: everyone with the address."},
			}, "site", "access"),
			// Changes who sees the site; the other value undoes it.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				access, err := stringArg(args, "access")
				if err != nil || (access != "anyone" && access != "specific") {
					return output{}, errors.New("access must be anyone or specific")
				}
				body, _ := json.Marshal(map[string]string{"access": access})
				return siteAccessOutput("set_site_access", c.do(http.MethodPut, path(name)+"/access", body, nil))
			},
		},
		{
			Name:        "list_site_viewers",
			Title:       "Who can open a site",
			Description: "Show who can open a site: its access (anyone or specific) and its named viewers. " + accessToolsWhen,
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				return siteAccessOutput("list_site_viewers", c.do(http.MethodGet, path(name)+"/access", nil, nil))
			},
		},
		{
			Name:  "grant_site_viewer",
			Title: "Let named people open a site",
			Description: "Add people, by the email they will sign in with, to a site's named viewers, and set its access to `specific`: from then on only they and the owner can open the site, after signing in on it with that email (an emailed code, or Google for a Google address). One call does it: \"make my family page only visible to mom@example.com and dad@example.com\" is grant_site_viewer with both emails. " +
				accessToolsWhen + " ASK THE PERSON FIRST and use exactly the emails they gave; never guess an address. Tell them to send the people the site's address and to sign in with the email named here.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"emails": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"},
					"description": "The emails to add, exactly as the person gave them (e.g. [\"mom@example.com\", \"dad@example.com\"])."},
			}, "site", "emails"),
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				raw, _ := args["emails"].([]any)
				var emails []string
				for _, e := range raw {
					if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
						emails = append(emails, s)
					}
				}
				if len(emails) == 0 {
					return output{}, errors.New("emails is required: the email addresses the person gave")
				}
				body, _ := json.Marshal(map[string][]string{"emails": emails})
				return siteAccessOutput("grant_site_viewer", c.do(http.MethodPost, path(name)+"/viewers", body, nil))
			},
		},
		{
			Name:        "revoke_site_viewer",
			Title:       "Stop a named person opening a site",
			Description: "Remove one person, by email, from a site's named viewers; their next page view is refused. Access does not change: removing the last viewer leaves the site open only to its owner until set_site_access sets it to anyone. Ask the person first.",
			InputSchema: object(map[string]any{
				"site":  str(siteDesc),
				"email": str("The email to remove, as list_site_viewers shows it."),
			}, "site", "email"),
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				email, err := stringArg(args, "email")
				if err != nil {
					return output{}, err
				}
				return siteAccessOutput("revoke_site_viewer", c.do(http.MethodDelete, path(name)+"/viewers/"+url.PathEscape(strings.TrimSpace(email)), nil, nil))
			},
		},
	}
}

// storageVisitorEmailsTool turns visitor_id values from a site's saved data
// into the email each person signed in with, for the owner.
func storageVisitorEmailsTool() Tool {
	return Tool{
		Name:  "storage_visitor_emails",
		Title: "Who sent these records",
		Description: "Turn visitor_id values from this site's saved data into the email each person signed in with, to answer \"who placed order 12?\" or \"who sent this?\". A visitor_id is on every row of a read-own SQLite table (storage_sql_query shows it) and, for you as the owner, on KV keys and stored files a visitor wrote (storage_get_kv, storage_list_kv_keys, storage_list_file_objects). " +
			"Only ids that saved something on this site resolve. The emails are personal data: show them to the person, never put them in a page or in saved data.",
		InputSchema: object(map[string]any{
			"site": str(siteDesc),
			"visitor_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "string"},
				"description": "The visitor_id values to look up, e.g. from SELECT id, visitor_id FROM orders WHERE id = 12."},
		}, "site", "visitor_ids"),
		OutputSchema: outObject(map[string]any{
			"site": outString(outSiteName),
			"visitors": outArray("One entry per id asked.", outObject(map[string]any{
				"visitor_id": outString("The id asked."),
				"email":      outString("The email the person signs in with."),
				"found":      outBool("false: the id saved nothing on this site, or its account is gone."),
			}, "visitor_id", "found")),
		}, "site", "visitors"),
		Annotations: readOnly(),
		run: func(c *call, args map[string]any) (output, error) {
			name, err := siteArg(args)
			if err != nil {
				return output{}, err
			}
			raw, _ := args["visitor_ids"].([]any)
			var ids []string
			for _, v := range raw {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					ids = append(ids, strings.TrimSpace(s))
				}
			}
			if len(ids) == 0 {
				return output{}, errors.New("visitor_ids is required: the visitor_id values from the rows")
			}
			res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/storage/visitors?id="+url.QueryEscape(strings.Join(ids, ",")), nil, nil)
			if !res.ok() {
				return output{}, restError("storage_visitor_emails", res)
			}
			var body struct {
				Visitors []struct {
					VisitorID string `json:"visitor_id"`
					Email     string `json:"email"`
					Found     bool   `json:"found"`
				} `json:"visitors"`
			}
			if err := json.Unmarshal(res.body, &body); err != nil {
				return output{}, errors.New("storage_visitor_emails: server returned invalid JSON")
			}
			list := make([]any, 0, len(body.Visitors))
			var lines []string
			for _, v := range body.Visitors {
				item := map[string]any{"visitor_id": v.VisitorID, "found": v.Found}
				if v.Found {
					item["email"] = v.Email
					lines = append(lines, v.VisitorID+": "+v.Email)
				} else {
					lines = append(lines, v.VisitorID+": not found on this site")
				}
				list = append(list, item)
			}
			return output{Text: strings.Join(lines, "\n"), Structured: map[string]any{"site": name, "visitors": list}}, nil
		},
	}
}
