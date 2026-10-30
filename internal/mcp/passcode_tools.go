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
			Title: "Check a site's passcode",
			Description: "Show whether a site has a passcode and when it was set. Never shows the passcode in chat; opens the passcode screen in apps that show screens. Changes nothing. " +
				"Use this before set_site_passcode, remove_site_passcode or sign_out_site_visitors, and when the person asks what their site's passcode is.",
			InputSchema: object(site, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				return c.passcodeStatus(name)
			},
		},
		{
			Name:  "set_site_passcode",
			Title: "Set or change a site's passcode",
			Description: "Put one passcode on a whole site, or change the one it has: every address of it shows an \"Enter the passcode\" page until a visitor enters the passcode. " +
				"Changing it replaces the earlier passcode, which is not kept, and every visitor who unlocked the site with the old one must enter the new one. " +
				passcodeNotLogin + " " +
				"Ask the person first; never ask for the passcode in chat or repeat one. Leave passcode out to open the passcode screen (apps that show screens), or use generate: true and the reply's link. Send passcode only when you already hold one for the person's own use. " +
				"The owner's key and this connector keep working on a protected site. " +
				"To take the passcode off use remove_site_passcode; to make visitors enter it again without changing it use sign_out_site_visitors.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"passcode": map[string]any{"type": "string", "description": "Send only a passcode you already hold for the person's own use, at least 6 characters. Never ask for it in chat. Leave out to open the screen or use generate."},
				"generate": map[string]any{"type": "boolean", "description": "true: Simple Host sets a new 6-digit passcode. The person sees it on the passcode screen or at the reply's dashboard link, never in chat."},
			}, "site"),
			// Replaces the earlier passcode for good and locks out everyone
			// who used it: a hard-to-reverse change.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				generate, _ := args["generate"].(bool)
				raw, isText := args["passcode"].(string)
				if v, present := args["passcode"]; present && v != nil && !isText {
					return output{}, errors.New(`passcode must be text in quotes, such as "583194"`)
				}
				var body []byte
				switch {
				case generate && strings.TrimSpace(raw) != "":
					return output{}, errors.New("send either passcode or generate: true, not both")
				case generate:
					body, _ = json.Marshal(map[string]bool{"generate": true})
				case strings.TrimSpace(raw) == "":
					return c.openPasscodeScreen(name)
				default:
					body, _ = json.Marshal(map[string]string{"passcode": raw})
				}
				out, err := passcodeOutput("set_site_passcode", name, c.do(http.MethodPut, path(name), body, nil))
				if err == nil && generate {
					out.Text = "A new passcode is set on " + name + ". " + c.passcodeDestination(name)
				}
				return out, err
			},
		},
		{
			Name:  "remove_site_passcode",
			Title: "Take the passcode off a site",
			Description: "Take the passcode off a site, so anyone with its address can open it again. Nothing on the site is deleted, and a new passcode can be set later with set_site_passcode. " +
				"ASK THE PERSON FIRST: the site becomes open to everyone.",
			InputSchema: object(site, "site"),
			Annotations: writes(true, true, true),
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
			Description: "Sign every visitor out of a passcode-protected site: each one sees \"Enter the passcode\" again and must enter the same passcode to open it. The passcode does not change and nothing is deleted. " +
				"Use this when the person thinks the passcode reached someone it should not have and wants a fresh start; to stop the old passcode working, change it with set_site_passcode instead. " +
				"Ask the person first. It fails on a site without a passcode.",
			InputSchema: object(site, "site"),
			// Every visitor's unlock ends at once and cannot be restored;
			// each one must be given the passcode again to get back in.
			Annotations: writes(true, false, true),
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
		SetAt     string `json:"passcode_set_at"`
		Note      string `json:"note"`
	}
	_ = json.Unmarshal(res.body, &lock)
	out := map[string]any{"site": name, "passcode_protected": lock.Protected}
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
	case (tool == "remove_site_passcode" || tool == "set_site_passcode" || tool == "passcode_screen") && strings.Contains(lock.Note, "nothing changed"):
		return output{Text: name + ": " + lock.Note, Structured: out}, nil
	case tool == "remove_site_passcode" && lock.Note != "":
		// The server's note says whether a passcode was actually removed.
		return output{Text: name + ": " + lock.Note, Structured: out}, nil
	case !lock.Protected:
		text = name + " has no passcode: anyone with its address can open it."
	case tool == "set_site_passcode":
		text = "Passcode set on " + name + ". Visitors see \"Enter the passcode\" until they enter it."
	default:
		text = name + " asks visitors for a passcode."
	}
	if lock.Note != "" && tool != "set_site_passcode" {
		text += " " + lock.Note
	}
	return output{Text: text, Structured: out}, nil
}

func (c *call) passcodeLink(name string) string {
	if c.caller.Handle == "" {
		// The admin key has no handle; its dashboard is one more click away.
		return "https://" + c.server.cfg.APIHost + "/dashboard"
	}
	return "https://" + c.server.cfg.APIHost + "/" + url.PathEscape(c.caller.Handle) + "?passcode=" + url.QueryEscape(name)
}

func (c *call) rendersPasscodeScreen() bool {
	return c.uiClient && !c.wrapped && c.server.passcodeScreenEnabled(c.caller)
}

func (c *call) passcodeDestination(name string) string {
	if c.rendersPasscodeScreen() {
		return "The passcode screen is open: the person can show, copy, or change it there. It is never shown in chat."
	}
	return "The person can see or change it at " + c.passcodeLink(name) + "; it is not shown in chat."
}

func (c *call) passcodeStatus(name string) (output, error) {
	out, err := passcodeOutput("get_site_passcode", name, c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/lock", nil, nil))
	if err != nil {
		return out, err
	}
	if out.Structured["passcode_protected"] == true {
		out.Text = name + " asks visitors for a passcode"
		if at, ok := out.Structured["passcode_set_at"].(string); ok {
			out.Text += ", set " + at
		}
		out.Text += "."
	}
	if out.Structured["passcode_protected"] == false {
		if c.rendersPasscodeScreen() {
			out.Text += " The passcode screen is open: the person can set one there."
		} else {
			out.Text += " The person can set one at " + c.passcodeLink(name) + "; it is not shown in chat."
		}
	} else {
		out.Text += " " + c.passcodeDestination(name)
	}
	return out, nil
}

func (c *call) openPasscodeScreen(name string) (output, error) {
	out, err := c.passcodeStatus(name)
	if err != nil {
		return out, err
	}
	switch {
	case c.rendersPasscodeScreen():
		out.Text = "The passcode screen is open: the person types or makes the passcode there. Nothing changed yet."
	case c.uiClient && c.wrapped && c.server.passcodeScreenEnabled(c.caller):
		out.Text = "Nothing changed. Call get_site_passcode for " + name + ": it opens the passcode screen."
	default:
		out.Text = "Nothing changed. The person can set it at " + c.passcodeLink(name) + ", or say \"make one for me\" and call again with generate: true. Never ask for the passcode in chat."
	}
	return out, nil
}
