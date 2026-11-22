package mcp

import (
	"net/http"
	"net/url"
)

// A site's secrets (host/secrets): keys an owner saves for an outside
// service, which the site's pages then use through Simple Host
// (SH.secret(name).fetch). Write-only: no tool takes or returns a value. The
// person types it on the write-only page request_secret links to.

// secretPolicy keeps a value argument (a key typed in the wrong place) out
// of analytics, on a refused call too.
var secretPolicy = toolPolicy{analytics: analyticsPolicy{secretValue: true}}

func secretTools() []Tool {
	listing := outObject(map[string]any{
		"secrets": outArray("The site's saved keys by name. A value is never shown.", outObject(map[string]any{
			"name":          outString("The secret's name."),
			"destination":   outString("The one outside host the key may be sent to."),
			"who":           outEnum("Who may make the site use the key.", "owner", "signed-in", "anyone"),
			"attach":        outEnum("How the key goes with each request: as a header or a query parameter.", "header", "query"),
			"attach_name":   outString("The header or query parameter that carries the key."),
			"attach_prefix": outString("What goes before the key, such as \"Bearer \"; often empty."),
			"created_at":    outString("When it was first saved (RFC3339)."),
			"updated_at":    outString("When it was last saved (RFC3339)."),
			"last_used_at":  map[string]any{"type": []any{"string", "null"}, "description": "When it was last used (RFC3339); null when never."},
			"use_count":     outInteger("How many times it was used."),
		}, "name", "destination", "who")),
		"limit": outInteger("The most secrets a site may hold."),
	}, "secrets")
	name := str("The secret's name: lowercase letters, digits, - and _ (at most 64), such as resend.")
	return []Tool{
		{
			policy: secretPolicy, OutputSchema: listing,
			Name: "list_secrets", Title: "List a site's saved keys",
			Description: "List the keys (secrets) saved for a site, by name, with the one outside host each may be sent to and who may use it. A key's value is never shown to anyone, here or anywhere.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run:         runListSecrets,
		},
		{
			policy: secretPolicy,
			OutputSchema: outObject(map[string]any{
				"url":  outString("The write-only page where the person types the key."),
				"note": outString("What to tell the person."),
			}, "url", "note"),
			Name: "request_secret", Title: "Ask the person to save a key",
			Description: "Get a link to the page where the person types a key (such as an API key for an AI or email service) for their site. Saves nothing. Give the person the link; never ask for the key in chat, never accept one there, and never put a key in page files. " +
				"The key is kept for one destination host and is never shown again; the site's pages use it with SH.secret(name).fetch(path, init) from auth.js, which Simple Host sends to that host with the key added. " +
				"Fill destination and attach from the service's API docs (Gemini: generativelanguage.googleapis.com, header x-goog-api-key; OpenAI: api.openai.com, header Authorization, prefix \"Bearer \"; Anthropic: api.anthropic.com, header x-api-key). Who may use it is the person's own choice on the page.",
			InputSchema: object(map[string]any{
				"site":          str(siteDesc),
				"name":          name,
				"destination":   str("The one outside host the key may ever be sent to, as a bare name such as api.openai.com: no https://, path or port."),
				"attach":        map[string]any{"type": "string", "enum": []any{"header", "query"}, "description": "How the service takes the key: a request header or a query parameter."},
				"attach_name":   str("The header or query parameter the service reads the key from, such as Authorization, x-goog-api-key, x-api-key or key."),
				"attach_prefix": str("Optional: what goes before the key, such as \"Bearer \" for Authorization; usually empty."),
				"who":           map[string]any{"type": "string", "enum": []any{"owner", "signed-in", "anyone"}, "description": "Optional: who the person said may make the site use the key. Shown on the page as a suggestion; the person chooses there."},
			}, "site", "name", "destination", "attach", "attach_name"),
			Annotations: readOnly(),
			run:         runRequestSecret,
		},
		{
			policy: secretPolicy, OutputSchema: listing,
			Name: "delete_secret", Title: "Delete a saved key",
			Description: "Delete one key saved for a site, for good: it cannot be shown or restored, and the person must type it again to use it. Ask the person first.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "name": str("The secret's name, from list_secrets.")}, "site", "name"),
			Annotations: writes(true, true, false),
			run:         runDeleteSecret,
		},
	}
}

func runListSecrets(c *call, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "list_secrets", http.MethodGet, sitePath(site, "/secrets"), nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func runRequestSecret(c *call, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	q := url.Values{"site": {site}}
	for _, key := range []string{"name", "destination", "attach", "attach_name"} {
		v, err := stringArg(args, key)
		if err != nil {
			return output{}, err
		}
		q.Set(key, v)
	}
	for _, key := range []string{"attach_prefix", "who"} {
		if v, _ := args[key].(string); v != "" {
			q.Set(key, v)
		}
	}
	if c.owner != "" {
		q.Set("owner", c.owner)
	}
	// The one call proves the caller may manage this site's secrets.
	if _, err := teamCall(c, "request_secret", http.MethodGet, sitePath(site, "/secrets"), nil); err != nil {
		return output{}, err
	}
	link := "https://" + c.server.cfg.APIHost + "/secrets?" + q.Encode()
	note := "Give the person this link. Never ask for the key in chat. They type it there, choose who may use it, and it is never shown again. Pages then use it with SH.secret(\"" + q.Get("name") + "\").fetch(path, init): " + link
	return output{Text: note, Structured: map[string]any{"url": link, "note": note}}, nil
}

func runDeleteSecret(c *call, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	name, err := stringArg(args, "name")
	if err != nil {
		return output{}, err
	}
	out, err := teamCall(c, "delete_secret", http.MethodDelete, sitePath(site, "/secrets/"+url.PathEscape(name)), nil)
	if err != nil {
		return output{}, err
	}
	return output{Text: "Deleted " + name + " from " + site + ".", Structured: out}, nil
}
