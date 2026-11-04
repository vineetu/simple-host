package mcp

// Account family: identity and showcase profile.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

func accountTools() []Tool {
	return []Tool{
		{
			Name:        "who_am_i",
			Title:       "Who am I signed in as",
			Description: "Return the Simple Host account this connection acts as: its handle (the <handle> in its page https://<handle>.simple-host.app/ and its site addresses https://<site>.<handle>.simple-host.app/) and its public page listing its sites. Email is returned only when include_email is true.",
			InputSchema: object(map[string]any{"include_email": map[string]any{"type": "boolean", "description": "Set true only when the person asks which email they are signed in with."}}),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				res := c.do(http.MethodGet, "/v1/me", nil, nil)
				if !res.ok() {
					return output{}, restError("who_am_i", res)
				}
				var me struct {
					Username           string         `json:"username"`
					Handle             string         `json:"handle"`
					HomeSite           *string        `json:"home_site"`
					FileUsage          map[string]any `json:"file_usage"`
					CanSetKeepVersions bool           `json:"can_set_keep_versions"`
					KeepVersions       int            `json:"keep_versions"`
					PublicPage         string         `json:"public_page"`
					Address            *struct {
						State        string `json:"state"`
						Address      string `json:"address"`
						ReadyInHours *int   `json:"ready_in_hours"`
						Note         string `json:"note"`
					} `json:"address"`
				}
				_ = json.Unmarshal(res.body, &me)
				out := map[string]any{"email": "", "home_site": me.HomeSite}
				out["can_set_keep_versions"], out["keep_versions"] = me.CanSetKeepVersions, me.KeepVersions
				if me.FileUsage != nil {
					out["file_usage"] = me.FileUsage
				}
				text := "Signed in to Simple Host."
				if include, _ := args["include_email"].(bool); include {
					out["email"] = me.Username
					text = "Signed in to Simple Host as " + me.Username + "."
				}
				if me.FileUsage != nil {
					text += " " + fmt.Sprint(me.FileUsage["message"]) + "."
				}
				if me.Handle != "" {
					page := me.PublicPage
					if page == "" {
						page = c.server.cfg.ContentOrigin + "/" + me.Handle
					}
					out["handle"], out["public_page"] = me.Handle, page
					text += " Handle: " + me.Handle + ". Public page: " + page
				}
				if a := me.Address; a != nil && a.State != "" {
					addr := map[string]any{"state": a.State, "address": a.Address}
					if a.ReadyInHours != nil {
						addr["ready_in_hours"] = *a.ReadyInHours
					}
					if a.Note != "" {
						addr["note"] = a.Note
						text += " " + a.Note
					}
					out["address"] = addr
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name: "set_bio", Title: "Set my showcase bio",
			Description: "Set the short plain-text bio shown on the person's public page (https://<handle>.simple-host.app/, the showcase of their sites). An empty string clears it; the limit is about 280 characters. " +
				"It replaces the current bio, which is not kept anywhere, so ask the person before replacing or clearing a bio they wrote (their public page shows the current one).",
			InputSchema: object(map[string]any{"bio": str("Plain text, or empty to clear the bio.")}, "bio"),
			// Overwrites the earlier bio with no copy kept: hard to reverse.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				bio, ok := args["bio"].(string)
				if !ok {
					return output{}, fmt.Errorf("bio must be a string")
				}
				body, _ := json.Marshal(map[string]any{"bio": bio})
				res := c.do(http.MethodPut, "/v1/me/bio", body, nil)
				if !res.ok() {
					return output{}, restError("set_bio", res)
				}
				var result map[string]any
				_ = json.Unmarshal(res.body, &result)
				return output{Text: "Showcase bio updated.", Structured: result}, nil
			},
		},
		{
			Name: "set_showcase_site", Title: "Pin or order a showcase site",
			Description: "Pin a site to the top of the person's public page (the showcase of their sites), or set its position. Pinned sites come first, then smaller order numbers. Unlisted, offline, and passcode-protected sites never appear there, pinned or not. Supply pinned, order, or both.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "pinned": map[string]any{"type": "boolean", "description": "true pins the site to the top of the showcase; false unpins it."}, "order": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000, "description": "Manual position among the pinned or unpinned sites: smaller numbers come first."}}, "site"),
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				payload := map[string]any{}
				if v, ok := args["pinned"]; ok {
					payload["pinned"] = v
				}
				if v, ok := args["order"]; ok {
					payload["order"] = v
				}
				if len(payload) == 0 {
					return output{}, fmt.Errorf("supply pinned and/or order")
				}
				body, _ := json.Marshal(payload)
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/showcase", body, nil)
				if !res.ok() {
					return output{}, restError("set_showcase_site", res)
				}
				var result map[string]any
				_ = json.Unmarshal(res.body, &result)
				return output{Text: "Showcase pin and order updated.", Structured: result}, nil
			},
		},
	}
}
