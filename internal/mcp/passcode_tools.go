package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// A site passcode (handler/passcode.go): one tool per operation, so each
// carries its own annotations (the OpenAI scan asks for this). The
// justifications are in openai-plugin/SUBMISSION.md.

const passcodeNotLogin = "It is a shared passcode, not a login: anyone given it can open the site and pass it on, and it does not make saved data private per person."

func passcodeTools() []Tool {
	path := func(name string) string { return "/v1/sites/" + url.PathEscape(name) + "/lock" }
	site := map[string]any{"site": str(siteDesc)}
	return []Tool{
		{
			Name:  "get_site_passcode",
			Title: "Show a site's passcode",
			Description: "Show whether a site asks visitors for a passcode and, when it does, the current passcode and when it was set. Changes nothing. " +
				"Use this before set_site_passcode, remove_site_passcode or sign_out_site_visitors, and when the person asks what their site's passcode is.",
			InputSchema: object(site, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				return passcodeOutput("get_site_passcode", name, c.do(http.MethodGet, path(name), nil, nil))
			},
		},
		{
			Name:  "set_site_passcode",
			Title: "Set or change a site's passcode",
			Description: "Put one passcode on a whole site, or change the one it has: every address of it shows a plain \"This site is protected\" page until a visitor enters the passcode. " +
				"Changing it replaces the earlier passcode, which is not kept, and every visitor who unlocked the site with the old one must enter the new one. " +
				passcodeNotLogin + " " +
				"ASK THE PERSON FIRST, and use the passcode the person chose; if they ask you to pick one, choose a 6-digit code and tell them what it is. " +
				"A passcode typed in chat stays in the conversation. The owner's key and this connector keep working on a protected site. " +
				"To take the passcode off use remove_site_passcode; to make visitors enter it again without changing it use sign_out_site_visitors.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"passcode": map[string]any{"type": "string", "description": "The passcode the person chose: at least 6 characters, any characters, digits only is fine."},
			}, "site", "passcode"),
			// Replaces the earlier passcode for good and locks out everyone
			// who used it: a hard-to-reverse change.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				raw, _ := args["passcode"].(string)
				if strings.TrimSpace(raw) == "" {
					return output{}, errors.New("passcode is required: use the passcode the person chose")
				}
				body, _ := json.Marshal(map[string]string{"passcode": raw})
				return passcodeOutput("set_site_passcode", name, c.do(http.MethodPut, path(name), body, nil))
			},
		},
		{
			Name:  "remove_site_passcode",
			Title: "Take the passcode off a site",
			Description: "Take the passcode off a site, so anyone with its address can open it again. Nothing on the site is deleted, and a new passcode can be set later with set_site_passcode. " +
				"ASK THE PERSON FIRST: the site becomes open to everyone.",
			InputSchema: object(site, "site"),
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				return passcodeOutput("remove_site_passcode", name, c.do(http.MethodDelete, path(name), nil, nil))
			},
		},
		{
			Name:  "sign_out_site_visitors",
			Title: "Make every visitor enter the passcode again",
			Description: "Sign every visitor out of a passcode-protected site: each one sees \"This site is protected\" again and must enter the same passcode to open it. The passcode does not change and nothing is deleted. " +
				"Use this when the person thinks the passcode reached someone it should not have and wants a fresh start; to stop the old passcode working, change it with set_site_passcode instead. " +
				"Ask the person first. It fails on a site without a passcode.",
			InputSchema: object(site, "site"),
			Annotations: writes(false, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				return passcodeOutput("sign_out_site_visitors", name, c.do(http.MethodPost, path(name)+"/sign-out-everyone", nil, nil))
			},
		},
	}
}

// passcodeOutput turns a /lock answer into the tool result shared by the
// passcode tools.
func passcodeOutput(tool, name string, res upstreamResult) (output, error) {
	if !res.ok() {
		return output{}, restError(tool, res)
	}
	var lock struct {
		Protected bool   `json:"passcode_protected"`
		Passcode  string `json:"passcode"`
		SetAt     string `json:"passcode_set_at"`
		Note      string `json:"note"`
	}
	_ = json.Unmarshal(res.body, &lock)
	out := map[string]any{"site": name, "passcode_protected": lock.Protected}
	if lock.Passcode != "" {
		out["passcode"] = lock.Passcode
	}
	if lock.SetAt != "" {
		out["passcode_set_at"] = lock.SetAt
	}
	if lock.Note != "" {
		out["note"] = lock.Note
	}
	var text string
	switch {
	case tool == "sign_out_site_visitors":
		text = "Everyone was signed out of " + name + "; visitors must enter the passcode again."
	case !lock.Protected:
		text = name + " has no passcode: anyone with its address can open it."
	case tool == "set_site_passcode":
		text = name + " now asks visitors for a passcode: " + lock.Passcode + ". Visitors see \"This site is protected\" until they enter it."
	case lock.Passcode != "":
		text = name + " asks visitors for a passcode: " + lock.Passcode + "."
	default:
		text = name + " asks visitors for a passcode."
	}
	if lock.Note != "" {
		text += " " + lock.Note
	}
	return output{Text: text, Structured: out}, nil
}
