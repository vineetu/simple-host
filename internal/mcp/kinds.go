package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Saved data, step 2: kinds. The owner's agent says once what each data name
// is: Page info (kind content: only the owner writes it, anyone reads it) or
// Submissions (kind entries: visitors send them; the owner reads all; each
// visitor sees, changes and withdraws their own; private unless made public).
// Personal (kind mine: one private record per signed-in visitor, which only
// that visitor reads) and Shared board (kind board: a list signed-in visitors
// add to and edit item by item; only the owner clears it) came in steps 3-4.
// A name nobody declared is Shared (public, signed-in visitors add to it),
// unless the install set SAVED_DATA_DEFAULT_KIND=declare_first.

const kindWords = "`entries` (Submissions: things visitors send, e.g. RSVPs, orders, sign-ups, votes, comments, feedback), `content` (Page info: text and settings only the owner writes and everyone reads, e.g. a menu, schedule, prices), " +
	"`mine` (Personal: one private record per signed-in visitor that follows them across devices, e.g. a habit tracker, saved progress, preferences; only that visitor reads it, the owner sees only how many people have one) " +
	"or `board` (Shared board: a list anyone reads and signed-in visitors add to, change and delete item by item, e.g. a shared shopping list, a kanban, a potluck sign-up; only the owner clears it)"

func kindTools() []Tool {
	return []Tool{
		{
			Name:  "declare_data",
			Title: "Say what a piece of saved data is",
			Description: "Declare (or change) what one data name on a site is, before the page saves to it: kind " + kindWords + ". " +
				"A name nobody declared is Shared: anyone reads it and anyone signed in adds to it, so declare anything with personal details (RSVPs, orders, sign-ups) as private Submissions and anything only the owner changes as Page info; when unsure, the stricter kind. " +
				"Submissions are private to the owner unless visibility is public; each visitor can see, change and withdraw only their own. one_per_person allows one entry per visitor (votes, one RSVP each). " +
				"notify emails the owner about new entries: daily (the default for private ones), each (batched, soon after they arrive) or off (the default for public ones). " +
				"Making private Submissions public (visibility public) shows everything already in them to anyone: it is refused (error confirm_public) until you send confirm_public true after the person agreed. A private name that holds entries never becomes Page info (error has_entries): use another name. " +
				"Personal and Shared board take no options. A name that already holds data never becomes Personal (error has_entries), and a Personal name that holds records never becomes another kind (error has_records): use another name.",
			InputSchema: object(map[string]any{
				"site":           str(siteDesc),
				"name":           str("The data name, e.g. `rsvps` or `menu` (letters, digits, - and _)."),
				"kind":           map[string]any{"type": "string", "enum": []string{"entries", "content", "mine", "board"}, "description": "entries (Submissions), content (Page info), mine (Personal) or board (Shared board)."},
				"visibility":     map[string]any{"type": "string", "enum": []string{"owner", "public"}, "description": "Submissions only: owner (only the owner reads them all; the default) or public (anyone reads them; who sent each stays private)."},
				"one_per_person": map[string]any{"type": "boolean", "description": "Submissions only: one entry per signed-in visitor, who changes it instead of adding another."},
				"notify":         map[string]any{"type": "string", "enum": []string{"off", "each", "daily"}, "description": "Submissions only: email the owner about new entries."},
				"confirm_public": map[string]any{"type": "boolean", "description": "The person agreed that the private entries this name holds become readable by anyone. Only after a confirm_public refusal was shown to them."},
			}, "site", "name", "kind"),
			// A setting; public visibility can put a list in front of anyone.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				dn, err := stringArg(args, "name")
				if err != nil {
					return output{}, err
				}
				kind, err := stringArg(args, "kind")
				if err != nil {
					return output{}, err
				}
				body := map[string]any{"kind": kind}
				for _, k := range []string{"visibility", "notify"} {
					v, err := optionalString(args, k)
					if err != nil {
						return output{}, err
					}
					if v != "" {
						body[k] = v
					}
				}
				for _, k := range []string{"one_per_person", "confirm_public"} {
					if raw, ok := args[k]; ok && raw != nil {
						b, isBool := raw.(bool)
						if !isBool {
							return output{}, errors.New(k + " must be true or false")
						}
						body[k] = b
					}
				}
				b, _ := json.Marshal(body)
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/data/"+url.PathEscape(dn)+"/kind", b, nil)
				if !res.ok() {
					return output{}, restError("declare_data", res)
				}
				var parsed struct {
					Kind         string `json:"kind"`
					Label        string `json:"label"`
					Visibility   string `json:"visibility"`
					OnePerPerson *bool  `json:"one_per_person"`
					Notify       string `json:"notify"`
					Message      string `json:"message"`
				}
				_ = json.Unmarshal(res.body, &parsed)
				out := map[string]any{"site": name, "name": dn, "kind": parsed.Kind, "label": parsed.Label}
				if parsed.Kind == "entries" {
					out["visibility"] = parsed.Visibility
					out["notify"] = parsed.Notify
					if parsed.OnePerPerson != nil {
						out["one_per_person"] = *parsed.OnePerPerson
					}
				}
				text := parsed.Message
				if text == "" {
					text = jsonText(out)
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name:        "list_data",
			Title:       "List a site's saved data",
			Description: "List every data name a site has, with its kind (Page info, Submissions, Personal, Shared board, or Shared when nobody declared it), how many items it holds (Personal: how many people have a record) and their size, whether it is private, one per person, the email setting, and who may save on the site. undeclared_names_take_saves says whether names nobody declared take saves (Shared) on this site. Personal records are never readable by the owner: only counts and sizes.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/data", nil, nil)
				if !res.ok() {
					return output{}, restError("list_data", res)
				}
				var parsed struct {
					Names []struct {
						Name         string `json:"name"`
						Count        int64  `json:"count"`
						Private      bool   `json:"private"`
						Deleted      int64  `json:"deleted"`
						Bytes        int64  `json:"bytes"`
						Kind         string `json:"kind"`
						Label        string `json:"label"`
						OnePerPerson bool   `json:"one_per_person"`
						Notify       string `json:"notify"`
					} `json:"names"`
					Savers struct {
						Mode  string   `json:"mode"`
						Allow []string `json:"allow"`
						Block []string `json:"block"`
					} `json:"savers"`
					Undeclared bool `json:"undeclared_names_take_saves"`
				}
				_ = json.Unmarshal(res.body, &parsed)
				names := make([]any, 0, len(parsed.Names))
				for _, n := range parsed.Names {
					item := map[string]any{"name": n.Name, "kind": n.Kind, "label": n.Label, "items": n.Count, "deleted": n.Deleted, "bytes": n.Bytes}
					if n.Kind != "content" && n.Kind != "mine" && n.Kind != "board" {
						item["private"] = n.Private
					}
					if n.Kind == "entries" {
						item["one_per_person"] = n.OnePerPerson
						item["notify"] = n.Notify
					}
					names = append(names, item)
				}
				allow, block := parsed.Savers.Allow, parsed.Savers.Block
				if allow == nil {
					allow = []string{}
				}
				if block == nil {
					block = []string{}
				}
				out := map[string]any{
					"site": name, "names": names, "undeclared_names_take_saves": parsed.Undeclared,
					"who_can_save": map[string]any{"mode": parsed.Savers.Mode, "allow": allow, "block": block},
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "update_data",
			Title:       "Save page info",
			Description: "Replace a Page info document (a name declared kind content: a menu, schedule, prices, dashboard numbers) with a new JSON object. Only the owner can; pages read it with SH.data(name).get(). The earlier version stays in data_history (restore_data with the collection set to the name). Read the current one first with read_collection and change only what was asked.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"name": str("The Page info name, e.g. `menu`."),
				"data": map[string]any{"type": "object", "description": "The whole new document."},
			}, "site", "name", "data"),
			// Replaces the published document: destructive and public (undo exists).
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				dn, err := stringArg(args, "name")
				if err != nil {
					return output{}, err
				}
				data, ok := args["data"].(map[string]any)
				if !ok {
					return output{}, errors.New("data must be a JSON object")
				}
				b, _ := json.Marshal(data)
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/data/"+url.PathEscape(dn), b, nil)
				if !res.ok() {
					return output{}, restError("update_data", res)
				}
				var parsed struct {
					Data json.RawMessage `json:"data"`
				}
				_ = json.Unmarshal(res.body, &parsed)
				var doc any
				_ = json.Unmarshal(parsed.Data, &doc)
				out := map[string]any{"site": name, "name": dn, "data": doc}
				return output{Text: "Saved " + dn + ".", Structured: out}, nil
			},
		},
		{
			Name:  "set_who_can_save",
			Title: "Choose who may save on a site",
			Description: "Choose who may save on a site (every save from its pages, all its data): anyone who signs in (mode anyone, the default) or only the listed people (mode listed, with allow: emails and whole domains written as @company.com). " +
				"block lists people (or domains) who may never save there, in either mode. This replaces both lists, so read them first with list_data and send the full lists. The owner can always save.",
			InputSchema: object(map[string]any{
				"site":  str(siteDesc),
				"mode":  map[string]any{"type": "string", "enum": []string{"anyone", "listed"}, "description": "anyone (anyone who signs in) or listed (only allow)."},
				"allow": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Emails and @domains who may save when mode is listed."},
				"block": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Emails and @domains who may never save."},
			}, "site", "mode"),
			Annotations: writes(false, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				mode, err := stringArg(args, "mode")
				if err != nil {
					return output{}, err
				}
				body := map[string]any{"mode": mode}
				for _, k := range []string{"allow", "block"} {
					raw, ok := args[k]
					if !ok || raw == nil {
						continue
					}
					list, isList := raw.([]any)
					if !isList {
						return output{}, errors.New(k + " must be a list of emails or @domains")
					}
					body[k] = list
				}
				b, _ := json.Marshal(body)
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/savers", b, nil)
				if !res.ok() {
					return output{}, restError("set_who_can_save", res)
				}
				var parsed struct {
					Mode    string   `json:"mode"`
					Allow   []string `json:"allow"`
					Block   []string `json:"block"`
					Message string   `json:"message"`
				}
				_ = json.Unmarshal(res.body, &parsed)
				if parsed.Allow == nil {
					parsed.Allow = []string{}
				}
				if parsed.Block == nil {
					parsed.Block = []string{}
				}
				out := map[string]any{"site": name, "mode": parsed.Mode, "allow": parsed.Allow, "block": parsed.Block}
				return output{Text: parsed.Message, Structured: out}, nil
			},
		},
		{
			Name:  "block_person",
			Title: "Block a person from saving on a site",
			Description: "Stop one person (or a whole @domain) from saving anything more on a site: give their email, or the collection and id of an entry they sent (read_collection shows the id and who sent it). " +
				"What they already sent stays; delete it separately with delete_collection_item if the person asks. Undo by sending the block list without them to set_who_can_save.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"email":      str("Their email, or @domain.com for a whole domain."),
				"collection": str("Or: the list their entry is in."),
				"id":         str("With collection: the entry's id from read_collection."),
			}, "site"),
			Annotations: writes(false, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				body := map[string]any{}
				email, err := optionalString(args, "email")
				if err != nil {
					return output{}, err
				}
				coll, err := optionalString(args, "collection")
				if err != nil {
					return output{}, err
				}
				switch {
				case email != "":
					body["email"] = email
				case coll != "":
					id := ""
					switch v := args["id"].(type) {
					case string:
						id = strings.TrimSpace(v)
					case float64:
						id = strconv.FormatFloat(v, 'f', -1, 64)
					}
					if id == "" {
						return output{}, errors.New("id is required with collection")
					}
					body["collection"], body["id"] = coll, id
				default:
					return output{}, errors.New("give email, or collection and id")
				}
				b, _ := json.Marshal(body)
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/savers/block", b, nil)
				if !res.ok() {
					return output{}, restError("block_person", res)
				}
				var parsed struct {
					Blocked string `json:"blocked"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(res.body, &parsed)
				out := map[string]any{"site": name, "blocked": parsed.Blocked}
				return output{Text: parsed.Message, Structured: out}, nil
			},
		},
	}
}

func kindOutputSchemas() map[string]map[string]any {
	kind := outEnum("entries (Submissions), content (Page info), mine (Personal) or board (Shared board).", "entries", "content", "mine", "board")
	return map[string]map[string]any{
		"declare_data": outObject(map[string]any{
			"site":           outString(outSiteName),
			"name":           outString("The data name."),
			"kind":           kind,
			"label":          outString("The kind in product words: Submissions, Page info, Personal or Shared board."),
			"visibility":     outEnum("Submissions: owner (only the owner reads them all) or public.", "owner", "public"),
			"one_per_person": outBool("Submissions: one entry per visitor."),
			"notify":         outEnum("Submissions: the owner's email about new entries.", "off", "each", "daily"),
		}, "site", "name", "kind", "label"),
		"list_data": outObject(map[string]any{
			"site": outString(outSiteName),
			"names": outArray("Every data name on the site.", outObject(map[string]any{
				"name":           outString("The data name."),
				"kind":           outString("entries (Submissions), content (Page info), mine (Personal), board (Shared board) or empty (not declared)."),
				"label":          outString("The kind in product words: Submissions, Page info, Personal, Shared board, Shared (not declared; public), Private list (not declared, made private) or Not set (not declared, takes no saves)."),
				"items":          outInteger("How many items it holds (Page info: 1 once saved; Personal: how many people have a record)."),
				"bytes":          outInteger("The size of what it holds now, in bytes."),
				"deleted":        outInteger("Items in its Recently deleted (list_deleted, restore_item)."),
				"private":        outBool("Whether only the owner reads it. Absent for Page info, Personal and Shared boards."),
				"one_per_person": outBool("Submissions: one entry per visitor."),
				"notify":         outString("Submissions: off, each or daily."),
			}, "name", "kind", "label", "items", "deleted")),
			"undeclared_names_take_saves": outBool("True when a name nobody declared is Shared (anyone reads it, anyone signed in adds to it): every site unless the install requires declaring first."),
			"who_can_save": outObject(map[string]any{
				"mode":  outEnum("anyone (anyone who signs in) or listed (only allow).", "anyone", "listed"),
				"allow": outArray("Emails and @domains who may save when mode is listed.", outString("An email or @domain.")),
				"block": outArray("Emails and @domains who may never save.", outString("An email or @domain.")),
			}, "mode", "allow", "block"),
		}, "site", "names", "undeclared_names_take_saves", "who_can_save"),
		"update_data": outObject(map[string]any{
			"site": outString(outSiteName),
			"name": outString("The Page info name."),
			"data": anyJSON("The document now saved."),
		}, "site", "name", "data"),
		"set_who_can_save": outObject(map[string]any{
			"site":  outString(outSiteName),
			"mode":  outEnum("anyone or listed.", "anyone", "listed"),
			"allow": outArray("Who may save when listed.", outString("An email or @domain.")),
			"block": outArray("Who may never save.", outString("An email or @domain.")),
		}, "site", "mode", "allow", "block"),
		"block_person": outObject(map[string]any{
			"site":    outString(outSiteName),
			"blocked": outString("The email or @domain now blocked."),
		}, "site", "blocked"),
	}
}
