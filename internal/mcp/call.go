package mcp

// REST replay bridge and result/error helpers shared by tool families.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type output struct {
	Meta       map[string]any
	Text       string
	Structured map[string]any
}

// call is one tools/call in flight: the original request (for the client
// address the rate limiters key on) and who it acts as.
type call struct {
	server         *Server
	policy         toolPolicy // resolved caller-visible tool policy
	orig           *http.Request
	caller         Caller
	handle         string
	textOnlyClient bool
	uiClient       bool // declared MCP Apps support (unknown is false)
	wrapped        bool
}

// upstreamResult is one in-process REST answer.
type upstreamResult struct {
	status int
	header http.Header
	body   []byte
}

func (u upstreamResult) ok() bool { return u.status >= 200 && u.status < 300 }

// do serves one REST request into the application router as the caller.
func (c *call) do(method, path string, body []byte, extra map[string]string) upstreamResult {
	req, err := http.NewRequestWithContext(c.orig.Context(), method, path, bytes.NewReader(body))
	if err != nil {
		return upstreamResult{status: http.StatusInternalServerError, body: []byte(`{"error":"could not build request"}`)}
	}
	req.Host = c.server.cfg.APIHost
	req.Header.Set("X-API-Key", c.caller.APIKey)
	req.Header.Set("X-Skill-Version", c.server.cfg.SkillVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	// The per-IP limiters must see the real client, or every connector call
	// would share one bucket (and escape the limit the REST caller meets).
	req.RemoteAddr = c.orig.RemoteAddr
	if xff := c.orig.Header.Get("X-Forwarded-For"); xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := &capture{header: http.Header{}, status: http.StatusOK}
	c.server.cfg.Upstream.ServeHTTP(rec, req)
	if hook := c.server.cfg.MutationResultHook; hook != nil {
		hook(c.orig.Context(), method, path, rec.status)
	}
	return upstreamResult{status: rec.status, header: rec.header, body: rec.body.Bytes()}
}

// ownHandle is the caller's URL handle, read once per call. An account gets a
// handle on its first deploy, so a brand-new one may not have it yet.
func (c *call) ownHandle() (string, error) {
	if c.handle != "" {
		return c.handle, nil
	}
	me := c.do(http.MethodGet, "/v1/me", nil, nil)
	if !me.ok() {
		return "", restError("who_am_i", me)
	}
	var body struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(me.body, &body)
	if body.Handle == "" {
		return "", errors.New("this account has no sites yet, so it has no site data to read or change; publish a site first with create_site")
	}
	c.handle = body.Handle
	return c.handle, nil
}

// capture is a minimal buffering ResponseWriter.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(status int) {
	if c.wrote {
		return
	}
	c.status, c.wrote = status, true
}
func (c *capture) Write(p []byte) (int, error) {
	c.wrote = true
	return c.body.Write(p)
}

// restError turns a refused REST call into a message a model can act on: the
// server's own error, plus what to do about it.
func restError(tool string, u upstreamResult) error {
	var payload struct {
		Hint   string `json:"hint"`
		Error  string `json:"error"`
		Code   string `json:"code"`
		Domain string `json:"domain"`
	}
	_ = json.Unmarshal(u.body, &payload)
	msg := payload.Error
	if payload.Hint != "" && (payload.Code == "site_too_large" || payload.Code == "site_total_too_large" || payload.Code == "account_storage_full") {
		msg += " " + payload.Hint
	}
	if msg == "" {
		msg = http.StatusText(u.status)
	}
	hint := instanceText(codeHint(payload.Code))
	if supportContact != "" {
		hint = strings.ReplaceAll(hint, "support@simple-host.app", supportContact)
	}
	if hint != "" && payload.Code == "use_custom_domain" && payload.Domain != "" {
		hint += " Its own address: " + payload.Domain + "."
	}
	if hint == "" && !selfExplainedCodes[payload.Code] {
		hint = statusHint(u.status, msg)
		if u.status == http.StatusTooManyRequests {
			if seconds, err := strconv.Atoi(u.header.Get("Retry-After")); err == nil && seconds >= 0 {
				hint = fmt.Sprintf("Rate limited. Wait %d seconds before trying again; do not retry in a loop.", seconds)
			}
		}
	}
	out := fmt.Sprintf("%s failed (HTTP %d): %s", tool, u.status, msg)
	if payload.Code != "" {
		out += " [" + payload.Code + "]"
	}
	if hint != "" {
		out += "\n" + hint
	}
	return &refusalError{text: out, code: payload.Code, status: u.status}
}

// refusalError is a REST refusal with the code the server named, kept apart
// from the text so nothing a caller sent can pass for a code.
type refusalError struct {
	text, code string
	status     int
}

func (e *refusalError) Error() string { return e.text }

// codedError is a refusal made here rather than by the server, so analytics
// can tell a wrong site name from a wrong argument.
func codedError(code, text string) error { return &refusalError{text: text, code: code} }

// CodedError lets another package in this binary (the visitor tool bridge in
// internal/handler, which runs outside the MCP boundary as a site's own
// visitor rather than its owner) make a refusal that codeHints/
// selfExplainedCodes and analytics treat exactly like one made in this
// package: the code travels with the error instead of only appearing as text
// a human would have to parse back out. The caller writes the complete,
// actionable text itself (this package's own direct codedError calls do the
// same; only restError pulls a hint from codeHints automatically); add the
// code to codeHints or selfExplainedCodes too, or analytics still records it
// as tool_error.
func CodedError(code, text string) error { return codedError(code, text) }

// RefusalCode returns the code of an error made with CodedError or
// codedError (including one wrapped by fmt.Errorf's %w or errors.Join), or
// "" when err carries none. Another package uses this to recover a code
// after it has sanitized or re-wrapped the error text.
func RefusalCode(err error) string {
	var refusal *refusalError
	if errors.As(err, &refusal) {
		return refusal.code
	}
	return ""
}

// selfExplainedCodes name refusals whose message already says what happened
// and what to do (the storage SQL errors), so no generic hint is added.
// Analytics records them like codes with hints.
var selfExplainedCodes = map[string]bool{
	"sql_request": true, "sql_params": true, "sql_syntax": true, "sql_multiple_statements": true,
	"sql_param_count": true, "sql_not_available": true, "sql_use_query": true, "sql_use_execute": true,
	"sql_use_schema": true, "sql_no_such_table": true, "sql_no_such_column": true, "sql_no_such_function": true,
	"sql_unique_failed": true, "sql_not_null_failed": true, "sql_check_failed": true, "sql_foreign_key_failed": true,
	"sql_constraint_failed": true, "sql_visitor_rule": true, "sql_timeout": true, "sql_value_too_large": true,
	"sql_error": true, "invalid_site_name": true, "invalid_argument": true,
}

// codeHints says what to do about a refusal the server named with a code.
// One HTTP status carries several meanings (a 409 is a taken address, an
// append-only list or an existing site), so the code decides the hint; the
// status only fills in when a refusal carries no code this table knows.
var codeHints = map[string]string{
	"redirects_invalid":              "Fix the listed _redirects line errors and publish again.",
	"site_exists":                    "A site of that name already exists in this account. Use update_site to publish a new version of it, or pick another name.",
	"recently_deleted":               "That name belongs to a site in Recently deleted. Ask the person whether to bring it back with restore_site, or pick another name.",
	"site_not_deleted":               "That site is live, not deleted; nothing to restore.",
	"domain_taken":                   "That address belongs to another site or another person. Ask the person for a different name; do not redeploy or retry the same address.",
	"invalid_name":                   "That name is not allowed: use 1 to 63 lowercase letters, digits and hyphens, with no hyphen at either end. Correct the name and call again.",
	"name_reserved":                  "That name is reserved by Simple Host and cannot be used. Ask the person for a different name.",
	"invalid_domain":                 "That is not a domain that can be connected. Send a bare hostname the person owns (e.g. shop.example.com or example.com), or a free <name>.simple-host.app address.",
	"site_quota_reached":             "This account has as many sites as it may hold. Tell the person; a site must be deleted (delete_site) before another can be created. Do not retry.",
	"site_total_too_large":           "The website exceeds its whole-file allowance after pruning. Follow the tips and check who_am_i and list_sites before trying smaller files.",
	"account_storage_full":           "The account is out of website file space. Follow the tips; ask the person which old sites to delete before deleting anything.",
	"keep_versions_fixed":            "Simple Host keeps your 4 latest versions. This account cannot change that count.",
	"site_too_large":                 "The site is bigger than this account may deploy (the error names the limit). Make it smaller (shrink or drop large images, video and unused files) and deploy again, or tell the person; do not retry the same files.",
	"custom_domain_required":         "This needs the site to have its own address first. Give it one with connect_domain (a free <name>.simple-host.app is active at once), then call again.",
	"use_custom_domain":              "This site saves on its own address, not the shared one. Tell the person; do not retry the same call.",
	"visitor_auth_required":          "Saving here needs a visitor signed in on the site's own address (visitor sign-in through auth.js); an agent cannot do it. Tell the person rather than retrying.",
	"sign_in_required":               "This resource takes that request only from a visitor signed in on the site's own address (SH.requireSignIn in the page). Through this connector the owner reads and writes with the storage_* tools.",
	"site_not_found":                 "No site of that name in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here.",
	"not_found":                      "Nothing of that name here. Check the site with list_sites and the resource with storage_list_resources.",
	"missing_api_key":                "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"wrong_auth_header":              "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"invalid_api_key":                "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"deploy_only_key":                "This key is deploy-only: it can create, update, roll back and list sites and make preview links, nothing else. Tell the person this needs a full key (the Keys panel on their Simple Host page makes one); do not retry.",
	"key_expired":                    "This API key has expired. Ask the person to reconnect Simple Host in their app; never ask for a key in chat.",
	"key_expired_idle":               "This API key stopped working because it went unused too long. Ask the person to reconnect Simple Host in their app; never ask for a key in chat.",
	"invalid_token":                  "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"site_suspended":                 "The operator has taken this site down, and changes to it are refused until it is restored. Tell the person; do not retry.",
	"account_suspended":              "This account is suspended by the operator. Tell the person to contact support@simple-host.app; do not retry.",
	"preview_unavailable":            "This site has no address of its own to show a preview on. Tell the person; the version can still be made live with rollback_site.",
	"version_not_found":              "No kept version of that number on this site. list_versions shows the versions that exist.",
	"version_live":                   "That version is live, so it cannot be discarded; only versions that were never live can be.",
	"version_published":              "That version has been live, so it stays as history; only never-live versions can be discarded.",
	"site_offline":                   "The owner has taken this site offline, so visitors cannot save to it. Put it back online with set_site_offline if the person wants that; the owner's own changes still work.",
	"site_locked":                    "This site asks visitors for a passcode; the owner's key and this connector still work. Ask the person to reconnect Simple Host in their app; never ask for a key in chat.",
	"passcode_locked":                "Too many wrong passcode tries from this visitor connection; the message above says how long the wait is. Tell the person; do not keep retrying the same or another guess.",
	"passcodes_not_enabled":          "This server cannot put a passcode on a site (whoever runs it has not turned passcodes on). Tell the person; do not retry.",
	"passcode_needs_own_address":     "A passcode works only on a site served at its own address (https://<site>.<handle>.simple-host.app/). Tell the person; do not retry.",
	"invalid_passcode":               "That passcode is too short, too long, or has control characters (the error above names this server's rule). The person can choose another on the passcode screen or in the dashboard; never ask for it in chat.",
	"no_passcode":                    "This site has no passcode, so there is nobody to sign out. Nothing to do; tell the person.",
	"site_private":                   "This site is open only to its owner and named viewers; the owner's key and this connector still work. Tell the person; do not retry.",
	"passcode_set":                   "This site has a passcode, and a site has a passcode or named viewers, not both. Ask the person whether to remove the passcode (remove_site_passcode) and use named viewers instead; do not remove it without a yes.",
	"named_viewers_set":              "This site is open only to named viewers, and a site has named viewers or a passcode, not both. Ask the person whether to open it to anyone (set_site_access access anyone) first; do not change it without a yes.",
	"named_viewers_need_own_address": "Named viewers work only on a site served at its own address (https://<site>.<handle>.simple-host.app/). Tell the person; do not retry.",
	"named_viewers_unavailable":      "Named viewers are not offered here. Tell the person; do not retry.",
	"sign_in_unavailable":            "Visitors cannot sign in on this server (no email codes or Google set up), so named viewers cannot work here. Tell the person; do not retry.",
	"invalid_email":                  "That is not an email address. Ask the person for the exact address, then call again.",
	"too_many_viewers":               "A site can name only so many viewers (the message says how many). Remove some with revoke_site_viewer after the person agrees, or tell the person.",
	"viewer_not_found":               "That email is not a named viewer of this site; list_site_viewers shows who is.",
	"invalid_visitor_id":             "Visitor ids are the visitor_id values from the rows (like 1b4e28ba-2fa1-11d2-883f-0016d3cca427), 1 to 100 per call.",
	"invalid_json":                   "The data holds text the database cannot store: a NUL character (\\u0000), half of a surrogate pair, or bytes that are not UTF-8. Remove it and call again.",
	"origin_not_allowed":             "The request came from a page that is not one of this site's own addresses. Write with the site owner's key and no Origin header (the connector does), or from the site's own page.",
	"account_auth_unavailable":       "Account sign-in answers only on simple-host.app itself; a page never uses it. For a site's visitors use visitor sign-in (auth.js: SH.mount and SH.requireSignIn).",
	"email_unavailable":              "This server cannot send sign-in emails, so visitors cannot sign in by emailed code here. Tell the person; do not retry.",
	"resource_not_found":             "No resource of that name on this site. storage_list_resources shows the exact names; storage_set_resource creates a new one.",
	"resource_kind_conflict":         "A resource of that name already exists with a different kind, and a kind cannot change. Use another name, or (after the person confirms) storage_delete_resource first.",
	"invalid_resource":               "The policy was refused. read is anyone, signed-in, own, or owner; write is anyone, signed-in, or owner; write_mode is full or add; read own with a visitor write policy needs write_mode add. Correct the body and call again.",
	"visitor_id_required":            "This database already holds rows without visitor identity, so it cannot switch to read own. Keep its current read policy, or create a new resource for each person's records.",
	"fixed_routes_required":          "Visitors cannot send SQL to this resource: a page uses SH.storage.sqlite(name).table(t).add and .list. SQL works only as the owner, through storage_sql_query and storage_sql_execute.",
	"add_only":                       "This resource is add-only for visitors: a page can add rows, keys, or files but never change or delete them. The owner changes rows with storage_sql_execute.",
	"invalid_sql":                    "SQLite refused the statement; the message above says why. Fix the statement (values go in params with ? placeholders) and call again.",
	"invalid_schema":                 "SQLite refused the schema statement; the message above says why. Send one CREATE, ALTER, or DROP statement per call and try again.",
	"invalid_params":                 "params must be a JSON array with one value for each ? placeholder, in order.",
	"invalid_rows":                   "The row was refused: send a JSON object of column names to values, for columns that exist in the table.",
	"invalid_reference":              "The row points at a parent row that does not exist or that belongs to another visitor; a visitor may reference only their own rows.",
	"row_conflict":                   "A row with that unique value already exists. Change the value, or read the existing row first.",
	"result_too_large":               "The query result is too large. Add a LIMIT (and ORDER BY) and page through it.",
	"resource_changed":               "The resource's policy changed since you read it. Call storage_list_resources and redo the change on the fresh policy.",
	"key_not_found":                  "No such key in this KV resource; storage_list_kv_keys shows the keys.",
	"file_not_found":                 "No such file in this files resource; storage_list_file_objects shows the paths.",
	"invalid_key":                    "That key is not allowed: use up to 256 characters, no control characters.",
	"invalid_path":                   "That path is not allowed: relative, no leading slash, no .., no segment starting with a dot, at most 16 segments.",
	"invalid_file":                   "The file was refused: it must be valid base64, not empty, and under the size limit (1 MiB through this tool).",
	"invalid_value":                  "The value must be valid JSON (a string, number, object, array, true, false, or null).",
	"site_full":                      "This site's storage allowance is full (KV and SQLite: 10,000,000 bytes together on Simple Host; one 1,000,000-byte pool on Simple Hack). storage_get_usage shows usage; remove old rows or keys only after the person agrees.",
	"sqlite_full":                    "This site's SQLite data has reached its allowance. storage_get_usage shows usage; remove old rows only after the person agrees.",
	"bucket_full":                    "This files resource holds as many files as it may (1,000). Delete old ones only after the person agrees.",
	"storage_busy":                   "The site is being published right now. Wait a few seconds and call once more; do not retry in a loop.",
	"file_exists":                    "A file at that path already exists. Choose another path, or ask the person before replacing it.",
	"key_exists":                     "That key already exists. Choose another key, or ask the person before replacing its value.",
	"name_taken":                     "That event name is already in use. Pick another; do not retry the same name.",
	"name_check_unavailable":         "The event name could not be checked. Call hack_check_event_name again in a moment.",
	"too_many_events_today":          "This person has created as many events as today allows. Tell them; do not retry.",
	"too_many_active_events":         "This person already has as many active events as this instance allows. Tell them; do not retry.",
	"instance_full":                  "This instance is at capacity, so it cannot take another event. Tell the person; do not retry.",
	"event_not_found":                "No event by that name that this person may use this way. Check hack_list_events. Someone who is not the organiser cannot manage the event. Do not retry.",
	"event_closed":                   "That event has ended and cannot be changed. Tell the person; do not retry.",
	"invalid_stage":                  "stage must be one of draft, open, building, closed, judging, results or archived.",
	"stage_not_available":            "That stage is not available. Read stages_offered from hack_get_event and choose one of those.",
	"invalid_rubric":                 "The rubric was refused. Use 1 to 10 criteria, weights as whole numbers from 0 to 100 that add up to 100, and max_points from 1 to 10. Fix it and call again.",
	"scores_locked":                  "Judging is locked at Results. Move the event back to Judging for scores, or Entries closed for setup changes.",
	"archive_needs_participants":     "Nobody has joined, so the event cannot be ended. Invite people first, or delete it from the event page.",
	"event_taken_down":               "The operator has taken this event down. Tell the person; do not retry.",
	"no_personal_sites":              "This connection manages events. It cannot publish a personal site or a team site. A team site needs a connection that chose that team.",
	"team_key_scope":                 "This connection publishes one team's site. It cannot manage events. Reconnect and choose Manage my events.",
	"unknown_tool":                   "No tool of that name exists on this connector. Call find_tools with the job, or pick one of the closest tools named above, instead of guessing the name again.",
}

func codeHint(code string) string { return codeHints[code] }

// supportContact replaces the hosted support address in hints on another
// install (Config.SupportContact; "" keeps it).
var supportContact string

// statusHint is the fallback for a refusal with no known code.
func statusHint(status int, msg string) string {
	switch {
	case status == http.StatusNotFound && strings.Contains(msg, "site not found"):
		return "No site of that name in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here."
	case status == http.StatusNotFound:
		return "Check the name against list_sites (and version numbers against list_versions)."
	case status == http.StatusConflict:
		return "The request conflicts with the site's current state; read the message above, check the site with get_site, and change the request rather than repeating it."
	case status == http.StatusBadRequest:
		return "The request was rejected as invalid; correct the arguments rather than retrying the same call."
	case status == http.StatusRequestEntityTooLarge:
		return "Too large. Send fewer or smaller files."
	case status == http.StatusPreconditionFailed:
		return "The data changed since you read it. Read it again and redo the change on the fresh copy."
	case status == http.StatusTooManyRequests:
		return "Rate limited. Wait a minute before trying again; do not retry in a loop."
	case status == http.StatusUnauthorized:
		return "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings."
	case status == http.StatusForbidden:
		return "This account is not allowed to do that."
	}
	return ""
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
