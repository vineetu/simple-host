package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Tool is one callable operation. run turns validated arguments into REST
// requests (through call.do) and shapes the answer; it holds no business rule
// of its own.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// OutputSchema describes structuredContent on a successful call (see
	// outputs.go). It is not sent to clients of 2025-03-26, which predates it.
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`

	run func(c *call, args map[string]any) (output, error)
}

type output struct {
	Text       string
	Structured map[string]any
}

// call is one tools/call in flight: the original request (for the client
// address the rate limiters key on) and who it acts as.
type call struct {
	server *Server
	orig   *http.Request
	caller Caller
	handle string
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
		msg = strings.TrimSpace(string(u.body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	if msg == "" {
		msg = http.StatusText(u.status)
	}
	hint := addressText(codeHint(payload.Code))
	if supportContact != "" {
		hint = strings.ReplaceAll(hint, "support@simple-host.app", supportContact)
	}
	if hint != "" && payload.Code == "use_custom_domain" && payload.Domain != "" {
		hint += " Its own address: " + payload.Domain + "."
	}
	if hint == "" {
		hint = statusHint(u.status, msg)
	}
	out := fmt.Sprintf("%s failed (HTTP %d): %s", tool, u.status, msg)
	if payload.Code != "" {
		out += " [" + payload.Code + "]"
	}
	if hint != "" {
		out += "\n" + hint
	}
	return errors.New(out)
}

// codeHints says what to do about a refusal the server named with a code.
// One HTTP status carries several meanings (a 409 is a taken address, an
// append-only list or an existing site), so the code decides the hint; the
// status only fills in when a refusal carries no code this table knows.
var codeHints = map[string]string{
	"site_exists":                "A site of that name already exists in this account. Use update_site to publish a new version of it, or pick another name.",
	"recently_deleted":           "That name belongs to a site in Recently deleted. Ask the person whether to bring it back with restore_site, or pick another name.",
	"site_not_deleted":           "That site is live, not deleted; nothing to restore.",
	"domain_taken":               "That address belongs to another site or another person. Ask the person for a different name; do not redeploy or retry the same address.",
	"invalid_name":               "That name is not allowed: use 1 to 63 lowercase letters, digits and hyphens, with no hyphen at either end. Correct the name and call again.",
	"name_reserved":              "That name is reserved by Simple Host and cannot be used. Ask the person for a different name.",
	"invalid_domain":             "That is not a domain that can be connected. Send a bare hostname the person owns (e.g. shop.example.com or example.com), or a free <name>.simple-host.app address.",
	"site_quota_reached":         "This account has as many sites as it may hold. Tell the person; a site must be deleted (delete_site) before another can be created. Do not retry.",
	"site_total_too_large":       "The website exceeds its whole-file allowance after pruning. Follow the tips and check who_am_i and list_sites before trying smaller files.",
	"account_storage_full":       "The account is out of website file space. Follow the tips; ask the person which old sites to delete before deleting anything.",
	"keep_versions_fixed":        "Simple Host keeps your 4 latest versions. This account cannot change that count.",
	"site_too_large":             "The site is bigger than this account may deploy (the error names the limit). Make it smaller (shrink or drop large images, video and unused files) and deploy again, or tell the person; do not retry the same files.",
	"custom_domain_required":     "This needs the site to have its own address first. Give it one with connect_domain (a free <name>.simple-host.app is active at once), then call again.",
	"use_custom_domain":          "This site saves on its own address, not the shared one. Tell the person; do not retry the same call.",
	"visitor_auth_required":      "Saving here needs a visitor signed in on the site's own address (visitor sign-in through auth.js); an agent cannot do it. Tell the person rather than retrying.",
	"sign_in_required":           "This resource takes that request only from a visitor signed in on the site's own address (SH.requireSignIn in the page). Through this connector the owner reads and writes with the storage_* tools.",
	"site_not_found":             "No site of that name in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here.",
	"not_found":                  "Nothing of that name here. Check the site with list_sites and the resource with storage_list_resources.",
	"missing_api_key":            "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"wrong_auth_header":          "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"invalid_api_key":            "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"deploy_only_key":            "This key is deploy-only: it can create, update, roll back and list sites and make preview links, nothing else. Tell the person this needs a full key (the Keys panel on their Simple Host page makes one); do not retry.",
	"key_expired":                "This API key has expired. Ask the person for a new key (the Keys panel on their Simple Host page makes one); do not retry.",
	"key_expired_idle":           "This API key stopped working because it went unused too long. Ask the person for a new key (the Keys panel on their Simple Host page makes one); do not retry.",
	"invalid_token":              "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings.",
	"site_suspended":             "The operator has taken this site down, and changes to it are refused until it is restored. Tell the person; do not retry.",
	"account_suspended":          "This account is suspended by the operator. Tell the person to contact support@simple-host.app; do not retry.",
	"preview_unavailable":        "This site has no address of its own to show a preview on. Tell the person; the version can still be made live with rollback_site.",
	"site_offline":               "The owner has taken this site offline, so visitors cannot save to it. Put it back online with set_site_offline if the person wants that; the owner's own changes still work.",
	"site_locked":                "This site asks visitors for a passcode; the owner's key and this connector still work. Tell the person; do not retry without their key.",
	"passcodes_not_enabled":      "This server cannot put a passcode on a site (whoever runs it has not turned passcodes on). Tell the person; do not retry.",
	"passcode_needs_own_address": "A passcode works only on a site served at its own address (https://<site>.<handle>.simple-host.app/). Tell the person; do not retry.",
	"invalid_passcode":           "The passcode must be at least 6 characters (any characters; digits only is fine), at most 128, with no control characters. Ask the person for one that fits, then call again.",
	"no_passcode":                "This site has no passcode, so there is nobody to sign out. Nothing to do; tell the person.",
	"invalid_json":               "The data holds text the database cannot store: a NUL character (\\u0000), half of a surrogate pair, or bytes that are not UTF-8. Remove it and call again.",
	"origin_not_allowed":         "The request came from a page that is not one of this site's own addresses. Write with the site owner's key and no Origin header (the connector does), or from the site's own page.",
	"account_auth_unavailable":   "Account sign-in answers only on simple-host.app itself; a page never uses it. For a site's visitors use visitor sign-in (auth.js: SH.mount and SH.requireSignIn).",
	"email_unavailable":          "This server cannot send sign-in emails, so visitors cannot sign in by emailed code here. Tell the person; do not retry.",
	"resource_not_found":         "No resource of that name on this site. storage_list_resources shows the exact names; storage_set_resource creates a new one.",
	"resource_kind_conflict":     "A resource of that name already exists with a different kind, and a kind cannot change. Use another name, or (after the person confirms) storage_delete_resource first.",
	"invalid_resource":           "The policy was refused. read is anyone, signed-in, own, or owner; write is anyone, signed-in, or owner; write_mode is full or add; read own with a visitor write policy needs write_mode add. Correct the body and call again.",
	"visitor_id_required":        "This database already holds rows without visitor identity, so it cannot switch to read own. Keep its current read policy, or create a new resource for each person's records.",
	"fixed_routes_required":      "Visitors cannot send SQL to this resource: a page uses SH.storage.sqlite(name).table(t).add and .list. SQL works only as the owner, through storage_sql_query and storage_sql_execute.",
	"add_only":                   "This resource is add-only for visitors: a page can add rows, keys, or files but never change or delete them. The owner changes rows with storage_sql_execute.",
	"invalid_sql":                "SQLite refused the statement; the message above says why. Fix the statement (values go in params with ? placeholders) and call again.",
	"invalid_schema":             "SQLite refused the schema statement; the message above says why. Send one CREATE, ALTER, or DROP statement per call and try again.",
	"invalid_params":             "params must be a JSON array with one value for each ? placeholder, in order.",
	"invalid_rows":               "The row was refused: send a JSON object of column names to values, for columns that exist in the table.",
	"invalid_reference":          "The row points at a parent row that does not exist or that belongs to another visitor; a visitor may reference only their own rows.",
	"row_conflict":               "A row with that unique value already exists. Change the value, or read the existing row first.",
	"result_too_large":           "The query result is too large. Add a LIMIT (and ORDER BY) and page through it.",
	"resource_changed":           "The resource's policy changed since you read it. Call storage_list_resources and redo the change on the fresh policy.",
	"key_not_found":              "No such key in this KV resource; storage_list_kv_keys shows the keys.",
	"file_not_found":             "No such file in this files resource; storage_list_file_objects shows the paths.",
	"invalid_key":                "That key is not allowed: use up to 256 characters, no control characters.",
	"invalid_path":               "That path is not allowed: relative, no leading slash, no .., no segment starting with a dot, at most 16 segments.",
	"invalid_file":               "The file was refused: it must be valid base64, not empty, and under the size limit (1 MiB through this tool).",
	"invalid_value":              "The value must be valid JSON (a string, number, object, array, true, false, or null).",
	"site_full":                  "This site's KV and SQLite allowance (1,000,000 bytes together) is full. storage_get_usage shows usage; remove old rows or keys only after the person agrees.",
	"sqlite_full":                "This site's SQLite data has reached its allowance. storage_get_usage shows usage; remove old rows only after the person agrees.",
	"bucket_full":                "This files resource holds as many files as it may (1,000). Delete old ones only after the person agrees.",
	"storage_busy":               "The site is being published right now. Wait a few seconds and call once more; do not retry in a loop.",
	"file_exists":                "A file at that path already exists. Choose another path, or ask the person before replacing it.",
	"key_exists":                 "That key already exists. Choose another key, or ask the person before replacing its value.",
	"name_taken":                 "That event name is already in use. Pick another; do not retry the same name.",
	"name_check_unavailable":     "The event name could not be checked. Call hack_check_event_name again in a moment.",
	"too_many_events_today":      "This person has created as many events as today allows. Tell them; do not retry.",
	"too_many_active_events":     "This person already has as many active events as this instance allows. Tell them; do not retry.",
	"instance_full":              "This instance is at capacity, so it cannot take another event. Tell the person; do not retry.",
	"event_not_found":            "No event by that name that this person may use this way. Check hack_list_events. Someone who is not the organiser cannot manage the event. Do not retry.",
	"event_closed":               "That event has ended and cannot be changed. Tell the person; do not retry.",
	"invalid_stage":              "stage must be one of draft, open, building, closed, judging, results or archived.",
	"stage_not_available":        "That stage is not available. Read stages_offered from hack_get_event and choose one of those.",
	"invalid_rubric":             "The rubric was refused. Use 1 to 10 criteria, weights as whole numbers from 0 to 100 that add up to 100, and max_points from 1 to 10. Fix it and call again.",
	"scores_locked":              "Judging is locked, so the rubric cannot be replaced. Tell the person; do not retry until they unlock it.",
	"archive_needs_participants": "Nobody has joined, so the event cannot be ended. Invite people first, or delete it from the event page.",
	"event_taken_down":           "The operator has taken this event down. Tell the person; do not retry.",
	"no_personal_sites":          "This connection manages events. It cannot publish a personal site or a team site. A team site needs a connection that chose that team.",
	"team_key_scope":             "This connection publishes one team's site. It cannot manage events. Reconnect and choose Manage my events.",
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

// ---- argument helpers -------------------------------------------------------

func object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func noArgs() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// Annotations follow the plugin directory's definitions, and every tool sets
// all three hints explicitly (a missing hint is a review failure):
//
//   - readOnlyHint: true only when the tool fetches and changes nothing.
//   - openWorldHint: true when the tool publishes to the public internet or
//     reaches an open-ended outside party (an arbitrary domain). Reading the
//     person's own account is a bounded workspace: false.
//   - destructiveHint: true when the tool can delete or overwrite something,
//     in any mode or through its defaults, even if a copy can be restored.
//
// The justification for each tool's values is in openai-plugin/SUBMISSION.md;
// keep the two in step.

// readOnly is a lookup in the person's own account.
func readOnly() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

// writes is a change. public says whether it puts something in front of the
// public (publishes, relists, re-addresses) or reaches an outside party.
func writes(destructive, idempotent, public bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": public}
}

func stringArg(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
}

func optionalString(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	return strings.TrimSpace(value), nil
}

// wholeNumber reads an integer argument, refusing fractions rather than
// truncating them (version 1.9 must not quietly mean version 1).
func wholeNumber(args map[string]any, key string, required bool, min, max int) (int, bool, error) {
	raw, present := args[key]
	if !present || raw == nil {
		if required {
			return 0, false, fmt.Errorf("%s is required", key)
		}
		return 0, false, nil
	}
	var value float64
	switch v := raw.(type) {
	case float64:
		value = v
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(v, "v")), 64)
		if err != nil {
			return 0, false, fmt.Errorf("%s must be a number, got %q", key, v)
		}
		value = f
	default:
		return 0, false, fmt.Errorf("%s must be a number, got %T", key, raw)
	}
	if value != math.Trunc(value) {
		return 0, false, fmt.Errorf("%s must be a whole number, got %v", key, value)
	}
	if value < float64(min) || value > float64(max) {
		return 0, false, fmt.Errorf("%s must be between %d and %d, got %v", key, min, max, value)
	}
	return int(value), true, nil
}

// siteArg reads and shape-checks a site name. The REST layer validates it
// again; checking here turns a refusal into a precise message.
func siteArg(args map[string]any) (string, error) {
	site, err := stringArg(args, "site")
	if err != nil {
		return "", err
	}
	site = strings.ToLower(site)
	for _, r := range site {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return "", fmt.Errorf("site %q is not a valid site name: use lowercase letters, numbers and hyphens only (e.g. \"birthday-rsvp\")", site)
		}
	}
	return site, nil
}

func stringMap(args map[string]any, key string) (map[string]string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object mapping file paths to contents", key)
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%q] must be a string, got %T", key, k, v)
		}
		out[k] = s
	}
	return out, nil
}

// ---- site helpers -----------------------------------------------------------

// restSite is the subset of the REST site object the tools report.
type restSite struct {
	FileBytes     int64  `json:"file_bytes"`
	Name          string `json:"name"`
	ActiveVersion int    `json:"active_version"`
	SiteURL       string `json:"site_url"`
	CustomDomain  string `json:"custom_domain"`
	DomainStatus  string `json:"domain_status"`
	Visibility    string `json:"visibility"`
	Offline       bool   `json:"offline"`
	// FamilyAddress is the site's address under its account's address
	// family, when one serves it (the address handed out after a domain).
	FamilyAddress string `json:"family_address"`
	// AddressState is present while the site is at its interim address.
	AddressState *struct {
		Note string `json:"note"`
	} `json:"address_state"`
}

// liveURL is the one address to give people: a connected, working custom
// domain wins, then the site's address under its account's address family,
// because the site's own address redirects there.
func (s restSite) liveURL() string {
	if s.CustomDomain != "" && s.DomainStatus == "active" {
		return "https://" + s.CustomDomain + "/"
	}
	if s.FamilyAddress != "" {
		return s.FamilyAddress
	}
	return s.SiteURL
}

func (s restSite) summary() map[string]any {
	m := map[string]any{
		"name":           s.Name,
		"file_bytes":     s.FileBytes,
		"url":            s.liveURL(),
		"active_version": s.ActiveVersion,
		"listed":         s.Visibility == "public",
	}
	if s.CustomDomain != "" {
		m["custom_domain"] = s.CustomDomain
		m["domain_status"] = s.DomainStatus
	}
	if s.AddressState != nil && s.AddressState.Note != "" {
		m["address_note"] = s.AddressState.Note
	}
	return m
}

func (c *call) listSites() ([]restSite, error) {
	res := c.do(http.MethodGet, "/v1/sites", nil, nil)
	if !res.ok() {
		return nil, restError("list_sites", res)
	}
	var sites []restSite
	if err := json.Unmarshal(res.body, &sites); err != nil {
		return nil, errors.New("list_sites: unexpected answer from the server")
	}
	return sites, nil
}

func (c *call) findSite(name string) (restSite, error) {
	sites, err := c.listSites()
	if err != nil {
		return restSite{}, err
	}
	for _, s := range sites {
		if s.Name == name {
			return s, nil
		}
	}
	return restSite{}, fmt.Errorf("no site named %q in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here", name)
}

// domainSummary is what a person needs about a site's custom domain: which
// domain, whether it is live, the one DNS record to add, and why it is not live
// yet. Binding times and the name of any site a pending binding was taken over
// from are the server's bookkeeping and stay out.
func domainSummary(site string, body []byte) map[string]any {
	var d struct {
		Domain         *string `json:"domain"`
		Status         *string `json:"status"`
		LastError      string  `json:"last_error"`
		Certificate    string  `json:"certificate_status"`
		PreviousDomain string  `json:"previous_domain"`
		FailingSince   string  `json:"failing_since"`
		DNS            *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns"`
		TXT *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns_txt"`
		PartnerDomain string `json:"partner_domain"`
		PartnerStatus string `json:"partner_status"`
		PartnerNote   string `json:"partner_note"`
		PartnerDNS    *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns_partner"`
	}
	_ = json.Unmarshal(body, &d)
	out := map[string]any{"site": site}
	if d.Domain == nil || *d.Domain == "" {
		out["domain"] = nil
		out["status"] = "none"
		return out
	}
	out["domain"] = *d.Domain
	if d.Status != nil {
		out["status"] = *d.Status
	}
	if d.DNS != nil {
		out["dns_record"] = map[string]any{"type": d.DNS.Type, "host": d.DNS.Host, "value": d.DNS.Value}
	}
	if d.TXT != nil {
		out["ownership_record"] = map[string]any{"type": d.TXT.Type, "host": d.TXT.Host, "value": d.TXT.Value}
	}
	if d.PartnerDomain != "" && d.PartnerDNS != nil {
		partner := map[string]any{
			"domain":     d.PartnerDomain,
			"status":     d.PartnerStatus,
			"dns_record": map[string]any{"type": d.PartnerDNS.Type, "host": d.PartnerDNS.Host, "value": d.PartnerDNS.Value},
		}
		if d.PartnerNote != "" {
			partner["note"] = d.PartnerNote
		}
		out["partner"] = partner
	}
	if d.LastError != "" {
		out["last_check"] = d.LastError
	}
	if d.Certificate != "" {
		out["certificate"] = d.Certificate
	}
	if d.PreviousDomain != "" {
		out["serving_at"] = "https://" + d.PreviousDomain + "/"
	}
	if d.FailingSince != "" {
		out["failing_since"] = d.FailingSince
	}
	if d.Status != nil && *d.Status == "active" {
		out["url"] = "https://" + *d.Domain + "/"
	}
	if d.Status != nil && *d.Status == "pending" {
		out["note"] = "Add both DNS records at the domain's registrar within " + span(lim().DomainUnprovenTTL) + ": dns_record points the domain here, and ownership_record (a TXT record) proves it is the person's; keep the TXT record in place afterwards. Until both are seen, the binding is provisional. Then the certificate is issued automatically and the domain goes live within minutes."
		if _, ok := out["partner"]; ok {
			out["note"] = out["note"].(string) + " Also add partner.dns_record so " + d.PartnerDomain + " forwards to " + *d.Domain + " (the one ownership record covers both)."
		}
	}
	return out
}

// familyDomain: d names an address family (*.<suffix>); returns the suffix.
func familyDomain(d string) (string, bool) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimSuffix(d, "/")
	suffix, ok := strings.CutPrefix(d, "*.")
	if !ok || suffix == "" {
		return "", false
	}
	return suffix, true
}

// familySummary is what a person needs about an address family.
func familySummary(body []byte) map[string]any {
	var f struct {
		Family     string `json:"family"`
		Suffix     string `json:"suffix"`
		SitePrefix string `json:"site_prefix"`
		Canonical  bool   `json:"canonical"`
		Status     string `json:"status"`
		Live       bool   `json:"live"`
		LastError  string `json:"last_error"`
		ExampleURL string `json:"example_url"`
		DNS        *struct {
			Type, Host, Value string
		} `json:"dns"`
		TXT *struct {
			Type, Host, Value string
		} `json:"dns_txt"`
		Certificate struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"certificate"`
	}
	_ = json.Unmarshal(body, &f)
	out := map[string]any{"domain": f.Family, "status": f.Status}
	if f.DNS != nil {
		out["dns_record"] = map[string]any{"type": f.DNS.Type, "host": f.DNS.Host, "value": f.DNS.Value}
	}
	if f.TXT != nil {
		out["ownership_record"] = map[string]any{"type": f.TXT.Type, "host": f.TXT.Host, "value": f.TXT.Value}
	}
	if f.Certificate.Status != "" {
		out["certificate"] = f.Certificate.Status
	}
	if f.LastError != "" {
		out["last_check"] = f.LastError
	}
	fam := map[string]any{"site_prefix": f.SitePrefix, "main_address": f.Canonical, "live": f.Live}
	if f.ExampleURL != "" {
		fam["example_url"] = f.ExampleURL
	}
	if f.Certificate.Note != "" {
		fam["certificate_note"] = f.Certificate.Note
	}
	out["family"] = fam
	if f.Live && f.ExampleURL != "" {
		out["url"] = f.ExampleURL
	}
	if f.Status == "pending" {
		out["note"] = "Add both DNS records at the domain's registrar within " + span(lim().FamilyUnprovenTTL) + ": dns_record (the wildcard) points every name under " + f.Suffix + " here, and ownership_record (a TXT record) proves it is the person's; keep the TXT record in place. Then every site X of the account answers at X." + f.Suffix + " once its certificate is live."
	}
	return out
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// itemArgs reads site, collection and a positive whole-number item id.
// idArg reads an optional id (a JSON number or a string of digits); "" when
// it is absent.
func idArg(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	var id string
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) || v <= 0 {
			return "", fmt.Errorf("%s must be an id from an earlier answer", key)
		}
		id = strconv.FormatInt(int64(v), 10)
	case string:
		id = strings.TrimSpace(v)
	default:
		return "", fmt.Errorf("%s must be an id from an earlier answer", key)
	}
	if id == "" {
		return "", nil
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
		return "", fmt.Errorf("%s must be an id from an earlier answer", key)
	}
	return id, nil
}

// restChange is one entry of a history answer.
type restChange struct {
	ID     int64           `json:"id"`
	ItemID *int64          `json:"item_id"`
	Op     string          `json:"op"`
	By     string          `json:"by"`
	ByKind string          `json:"by_kind"`
	At     string          `json:"at"`
	Size   int64           `json:"size"`
	Value  json.RawMessage `json:"value"`
}

func (e restChange) summary(withValue bool) map[string]any {
	m := map[string]any{"version": strconv.FormatInt(e.ID, 10), "op": e.Op, "by_kind": e.ByKind, "at": e.At, "size": e.Size}
	if e.By != "" {
		m["by"] = e.By
	}
	if e.ItemID != nil {
		m["item_id"] = strconv.FormatInt(*e.ItemID, 10)
	}
	if withValue {
		var v any
		_ = json.Unmarshal(e.Value, &v)
		m["value"] = v
	}
	return m
}

func itemArgs(args map[string]any) (site, coll, id string, err error) {
	if site, err = siteArg(args); err != nil {
		return
	}
	if coll, err = stringArg(args, "collection"); err != nil {
		return
	}
	var raw any = args["id"]
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) || v <= 0 {
			return "", "", "", errors.New("id must be the item id from read_collection")
		}
		id = strconv.FormatInt(int64(v), 10)
	default:
		if id, err = stringArg(args, "id"); err != nil {
			return
		}
		id = strings.TrimSpace(id)
	}
	if n, perr := strconv.ParseInt(id, 10, 64); perr != nil || n <= 0 {
		return "", "", "", errors.New("id must be the item id from read_collection")
	}
	return site, coll, id, nil
}

// ---- the tools ---------------------------------------------------------------

const siteDesc = "The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens."

// maxFileText bounds how much of one file read_site_file hands back, so one
// call cannot flood the conversation.
const maxFileText = 200 << 10

// Tools is the callable surface, in the order a first-time agent needs it.
func Tools() []Tool {
	tools := []Tool{
		{
			Name:        "who_am_i",
			Title:       "Who am I signed in as",
			Description: "Return the Simple Host account this connection acts as: its email, its handle (the <handle> in its page https://<handle>.simple-host.app/ and its site addresses https://<site>.<handle>.simple-host.app/) and its public page listing its sites.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
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
					DisplayName        string         `json:"display_name"`
					PublicPage         string         `json:"public_page"`
					Address            *struct {
						State        string `json:"state"`
						Address      string `json:"address"`
						ReadyInHours *int   `json:"ready_in_hours"`
						Note         string `json:"note"`
					} `json:"address"`
				}
				_ = json.Unmarshal(res.body, &me)
				out := map[string]any{"email": me.Username, "home_site": me.HomeSite}
				out["can_set_keep_versions"], out["keep_versions"] = me.CanSetKeepVersions, me.KeepVersions
				if me.FileUsage != nil {
					out["file_usage"] = me.FileUsage
				}
				text := "Signed in to Simple Host as " + me.Username + "."
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
				if me.DisplayName != "" {
					out["display_name"] = me.DisplayName
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
			Name:        "list_sites",
			Title:       "List my sites",
			Description: "List every site in this account with its live address, active version and whether it has its own domain. Call this before changing a site to get its exact name.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				sites, err := c.listSites()
				if err != nil {
					return output{}, err
				}
				items := make([]any, 0, len(sites))
				for _, s := range sites {
					item := s.summary()
					if s.Offline {
						item["offline"] = true
					}
					items = append(items, item)
				}
				out := map[string]any{"sites": items, "count": len(items)}
				if len(items) == 0 {
					return output{Text: "This account has no sites yet. Publish one with create_site.", Structured: out}, nil
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "get_site",
			Title: "Get one site and its files",
			Description: "Show one site: its live address, active version, custom domain, and the list of files in the live version with their sizes. " +
				"Call this before editing an existing site, then read_site_file for each file you will change.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				site, err := c.findSite(name)
				if err != nil {
					return output{}, err
				}
				out := site.summary()
				if site.ActiveVersion > 0 {
					res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(site.ActiveVersion)+"/files", nil, nil)
					if res.ok() {
						var listing struct {
							Files []struct {
								Path string `json:"path"`
								Size int64  `json:"size"`
							} `json:"files"`
						}
						_ = json.Unmarshal(res.body, &listing)
						files := make([]any, 0, len(listing.Files))
						for _, f := range listing.Files {
							files = append(files, map[string]any{"path": f.Path, "size": f.Size})
						}
						out["files"] = files
					}
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "read_site_file",
			Title: "Read a file from a site",
			Description: "Return the contents of one file of a site (the live version unless `version` is given). Use it to edit an existing site: read the file, change it, and deploy the full set of files again. " +
				"Text files come back as text; binary files (images, fonts) are only described, since update_site keeps nothing you do not resend — resend a binary file only if you have its bytes.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"path":    str("Path of the file from the site root, e.g. `index.html` or `css/style.css`."),
				"version": map[string]any{"type": "integer", "description": "Version number to read from. Defaults to the live version."},
			}, "site", "path"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				path, err := stringArg(args, "path")
				if err != nil {
					return output{}, err
				}
				version, given, err := wholeNumber(args, "version", false, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				if !given {
					site, err := c.findSite(name)
					if err != nil {
						return output{}, err
					}
					version = site.ActiveVersion
				}
				segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
				for i, s := range segments {
					segments[i] = url.PathEscape(s)
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version)+"/files/"+strings.Join(segments, "/"), nil, nil)
				if !res.ok() {
					return output{}, restError("read_site_file", res)
				}
				out := map[string]any{"site": name, "path": path, "version": version, "size": len(res.body)}
				ctype := res.header.Get("Content-Type")
				textual := utf8.Valid(res.body) && !bytes.ContainsRune(res.body, 0) &&
					!strings.HasPrefix(ctype, "image/") && !strings.HasPrefix(ctype, "font/") && !strings.HasPrefix(ctype, "audio/") && !strings.HasPrefix(ctype, "video/")
				if !textual {
					out["binary"] = true
					return output{Text: fmt.Sprintf("%s is a binary file (%s, %d bytes); its contents are not shown.", path, ctype, len(res.body)), Structured: out}, nil
				}
				content := string(res.body)
				if len(content) > maxFileText {
					content = content[:maxFileText]
					for !utf8.ValidString(content) {
						content = content[:len(content)-1]
					}
					out["truncated"] = true
				}
				out["content"] = content
				return output{Text: content, Structured: out}, nil
			},
		},
		{
			Name:  "create_site",
			Title: "Publish a new site",
			Description: "Publish a NEW website from files given inline, at a public address. Fails if this account already has a site of that name, so it never overwrites anything; to change an existing site use update_site. " +
				"`index.html` is required. Use relative links only (`css/style.css`, never `/css/style.css`), because a site can be served under a path as well as at a root. " +
				"The site is public to anyone with the returned URL as soon as this returns. " + deploySizeTips,
			InputSchema: object(map[string]any{
				"site":         str(siteDesc + " Pick a short, descriptive name."),
				"files":        filesSchema(),
				"files_base64": filesBase64Schema(),
			}, "site", "files"),
			// Publishes to the public web; creates only, so nothing is lost.
			Annotations: writes(false, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "create") },
		},
		{
			Name:  "update_site",
			Title: "Publish a new version of a site",
			Description: "Replace the live files of an EXISTING site with a new version, at its public address. The files you send are the COMPLETE new version: anything not included stops being served, so to change one page read the others with read_site_file and send them all again. " +
				"`index.html` is required; use relative links only. The previous version is kept and can be made live again with rollback_site. Fails if there is no site of that name (use create_site). " +
				deploySizeTips + " With `publish: false` the new version is only stored, not made live: visitors keep seeing the current one, and the answer carries a preview link (anyone with it can open it, for " + span(lim().PreviewLinkTTL) + ") to look at it first; make it live with rollback_site when the person is happy.",
			InputSchema: object(map[string]any{
				"site":         str(siteDesc),
				"files":        filesSchema(),
				"files_base64": filesBase64Schema(),
				"publish":      map[string]any{"type": "boolean", "description": "Default true: the new version goes live at once. false stores it without making it live, and returns a preview link."},
			}, "site", "files"),
			// Overwrites what is live (destructive, even though rollback_site
			// can restore it) and publishes to the public web.
			Annotations: writes(true, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "replace") },
		},
		{
			Name: "set_keep_versions", Title: "Set versions kept",
			Description: "Set a site's version count; older versions are removed for good. Only accounts enabled by the operator may change it. Other accounts get: Simple Host keeps your 4 latest versions. 0 uses the account default. The live version always stays.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "keep_versions": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000, "description": "How many versions to keep (the live one included); 0 goes back to the account default."}}, "site", "keep_versions"),
			Annotations: writes(true, false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]any{"keep_versions": args["keep_versions"]})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/keep-versions", body, nil)
				if !res.ok() {
					return output{}, restError("set_keep_versions", res)
				}
				var out map[string]any
				_ = json.Unmarshal(res.body, &out)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "list_versions",
			Title:       "List a site's versions",
			Description: "List the versions kept for a site, newest first, with which one is live. Call this before rollback_site.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions", nil, nil)
				if !res.ok() {
					return output{}, restError("list_versions", res)
				}
				var raw []struct {
					VersionNumber int    `json:"version_number"`
					CreatedAt     string `json:"created_at"`
					IsActive      bool   `json:"is_active"`
					Status        string `json:"status"`
				}
				_ = json.Unmarshal(res.body, &raw)
				sort.SliceStable(raw, func(i, j int) bool { return raw[i].VersionNumber > raw[j].VersionNumber })
				// When each version was published is what a person picks a
				// rollback by ("the one from yesterday"), so it stays; nothing
				// else about the stored version is shown.
				versions := make([]any, 0, len(raw))
				for _, v := range raw {
					item := map[string]any{"version": v.VersionNumber, "live": v.IsActive, "published_at": v.CreatedAt}
					if v.Status == "ready" && !v.IsActive {
						item["not_yet_live"] = true
					}
					versions = append(versions, item)
				}
				out := map[string]any{"site": name, "versions": versions}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "rollback_site",
			Title:       "Make an earlier version live",
			Description: "Make one of a site's kept versions live (visitors see it immediately): an earlier one, or one stored with update_site `publish: false` after the person has looked at its preview. Nothing is deleted; the current version stays available and can be restored the same way. Confirm the version with the person first.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The version number to make live, from list_versions."},
			}, "site", "version"),
			// Changes what the public sees; nothing is deleted and the
			// version it replaces can be made live again the same way.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]int{"version_number": version})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/active-version", body, nil)
				if !res.ok() {
					return output{}, restError("rollback_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				out := site.summary()
				return output{Text: fmt.Sprintf("Version %d of %s is now live at %s", site.ActiveVersion, name, site.liveURL()), Structured: out}, nil
			},
		},
		{
			Name:  "preview_version",
			Title: "Preview a version before it is live",
			Description: "Make a preview link for one of a site's kept versions (from list_versions): the person opens it in their browser to see that version exactly as visitors would, before making it live with rollback_site. " +
				"The link works for anyone who has it, for " + span(lim().PreviewLinkTTL) + " and only for that version; pages opened from it cannot save anything, and search engines do not index it. Give it to the person to click; do not post it anywhere public.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The version number to preview, from list_versions."},
			}, "site", "version"),
			// Not read-only: each call mints a new bearer link on the server.
			Annotations: writes(false, false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version)+"/preview-link", nil, nil)
				if !res.ok() {
					return output{}, restError("preview_version", res)
				}
				var link struct {
					URL       string `json:"url"`
					ExpiresAt string `json:"expires_at"`
					Live      bool   `json:"live"`
				}
				_ = json.Unmarshal(res.body, &link)
				if link.URL == "" {
					return output{}, errors.New("preview_version failed: the server returned no link; try again in a moment")
				}
				out := map[string]any{"site": name, "version": version, "live": link.Live, "url": link.URL, "expires_at": link.ExpiresAt}
				text := fmt.Sprintf("Preview of %s version %d (valid until %s): %s", name, version, link.ExpiresAt, link.URL)
				if !link.Live {
					text += fmt.Sprintf("\nIt is not live; rollback_site with version %d makes it live.", version)
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name:  "delete_site",
			Title: "Delete a site",
			Description: "DESTRUCTIVE: takes the site offline at once, with every version and all of its saved data (storage resources included). It stays in Recently deleted for " + span(lim().DeletedRetention) + ", where restore_site brings it back exactly as it was; after that it is gone for good. Its name stays taken until then. " +
				"Only call this after the person has explicitly confirmed, in this conversation, that they want this specific site deleted. Pass the site name twice: as `site` and as `confirm_name`.",
			InputSchema: object(map[string]any{
				"site":         str(siteDesc),
				"confirm_name": str("The same site name again, typed out, as confirmation."),
			}, "site", "confirm_name"),
			// Destructive (reversible only for 7 days). Acts only inside the
			// person's own account and publishes nothing, so it is not open-world.
			Annotations: writes(true, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				confirm, err := stringArg(args, "confirm_name")
				if err != nil {
					return output{}, err
				}
				if strings.ToLower(confirm) != name {
					return output{}, fmt.Errorf("confirm_name %q does not match site %q; nothing was deleted", confirm, name)
				}
				res := c.do(http.MethodDelete, "/v1/sites/"+url.PathEscape(name), nil, nil)
				if !res.ok() {
					return output{}, restError("delete_site", res)
				}
				return output{Text: "Deleted " + name + ". It is offline now and stays in Recently deleted for " + span(lim().DeletedRetention) + "; restore_site brings it back with all its versions and data.", Structured: map[string]any{"deleted": name, "restorable_days": int(lim().DeletedRetention / (24 * time.Hour))}}, nil
			},
		},
		{
			Name:        "list_deleted_sites",
			Title:       "List recently deleted sites",
			Description: "List the account's sites in Recently deleted: each was deleted within the last " + span(lim().DeletedRetention) + " and can be brought back with restore_site until its purge_at time, when it is removed for good.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				res := c.do(http.MethodGet, "/v1/me/deleted-sites", nil, nil)
				if !res.ok() {
					return output{}, restError("list_deleted_sites", res)
				}
				var body struct {
					Sites []struct {
						Name      string `json:"name"`
						DeletedAt string `json:"deleted_at"`
						PurgeAt   string `json:"purge_at"`
					} `json:"sites"`
				}
				_ = json.Unmarshal(res.body, &body)
				sites := make([]any, 0, len(body.Sites))
				lines := make([]string, 0, len(body.Sites))
				for _, d := range body.Sites {
					sites = append(sites, map[string]any{"name": d.Name, "deleted_at": d.DeletedAt, "purge_at": d.PurgeAt})
					lines = append(lines, d.Name+" (restorable until "+d.PurgeAt+")")
				}
				text := "Nothing in Recently deleted."
				if len(lines) > 0 {
					text = "Recently deleted: " + strings.Join(lines, "; ")
				}
				return output{Text: text, Structured: map[string]any{"sites": sites, "count": len(sites)}}, nil
			},
		},
		{
			Name:        "restore_site",
			Title:       "Restore a deleted site",
			Description: "Bring back a site from Recently deleted (see list_deleted_sites): same name, same address, every version, saved data and connected address, live again at once.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			// Serves the site again at its public address; nothing is lost.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/restore", nil, nil)
				if !res.ok() {
					return output{}, restError("restore_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				return output{Text: "Restored " + site.Name + ". Live again at " + site.liveURL(), Structured: site.summary()}, nil
			},
		},
		{
			Name:        "rename_site",
			Title:       "Rename a site",
			Description: "Give a site a new name, which changes its address. Links to the old address keep working: they redirect to the new one until a new site is created with the old name. A connected custom domain stays attached.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"new_name": str("The new site name: lowercase letters, numbers and hyphens."),
			}, "site", "new_name"),
			// Serves the site at a new public address. Nothing is deleted and
			// renaming back restores the old address.
			Annotations: writes(false, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				newName, err := stringArg(args, "new_name")
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"name": strings.ToLower(newName)})
				res := c.do(http.MethodPatch, "/v1/sites/"+url.PathEscape(name), body, nil)
				if !res.ok() {
					return output{}, restError("rename_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				return output{Text: "Renamed to " + site.Name + ". New address: " + site.liveURL(), Structured: site.summary()}, nil
			},
		},
		{
			Name: "set_bio", Title: "Set my showcase bio",
			Description: "Set the short plain-text bio shown on the person's public page (https://<handle>.simple-host.app/, the showcase of their sites). An empty string clears it; the limit is about 280 characters.",
			InputSchema: object(map[string]any{"bio": str("Plain text, or empty to clear the bio.")}, "bio"),
			Annotations: writes(false, true, true),
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
			Annotations: writes(false, true, true),
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
		{
			Name:        "set_home_page",
			Title:       "Choose my home page",
			Description: "Make one of the person's sites their home page, served at their personal address https://<handle>.simple-host.app/ instead of the showcase (the list of their sites); site null brings the showcase back. The site's own address keeps working too. If that site goes offline or is deleted, the showcase shows again.",
			InputSchema: object(map[string]any{"site": map[string]any{"type": []string{"string", "null"}, "description": "Your site's name, or null for the showcase."}}, "site"),
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				site, ok := args["site"]
				if !ok {
					return output{}, fmt.Errorf("site is required (name or null)")
				}
				if site != nil {
					if _, ok := site.(string); !ok {
						return output{}, fmt.Errorf("site must be a name or null")
					}
				}
				body, _ := json.Marshal(map[string]any{"site": site})
				res := c.do(http.MethodPut, "/v1/me/home", body, nil)
				if !res.ok() {
					return output{}, restError("set_home_page", res)
				}
				var result map[string]any
				_ = json.Unmarshal(res.body, &result)
				return output{Text: "Home page updated.", Structured: result}, nil
			},
		},
		{
			Name:  "set_visibility",
			Title: "Show or hide a site on my public page",
			Description: "Choose whether a site is listed on the account's public page (https://<handle>.simple-host.app/). " +
				"This is NOT privacy: an unlisted site is still public to anyone with its address; never describe unlisted as private. To ask visitors for a passcode on the whole site, use set_site_passcode.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"visibility": map[string]any{"type": "string", "enum": []string{"public", "unlisted"}, "description": "`public` lists it on the public page; `unlisted` leaves it off (still reachable by its address)."},
			}, "site", "visibility"),
			// Adds a site to, or removes it from, a public listing page.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				vis, err := stringArg(args, "visibility")
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"visibility": vis})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/visibility", body, nil)
				if !res.ok() {
					return output{}, restError("set_visibility", res)
				}
				return output{Text: name + " is now " + strings.ToLower(vis) + ".", Structured: map[string]any{"site": name, "visibility": strings.ToLower(vis)}}, nil
			},
		},
		{
			Name:  "set_site_offline",
			Title: "Take a site offline or back online",
			Description: "Take a site offline (`offline: true`): every address of it shows a plain \"This site is offline\" page and visitors can no longer save anything (RSVPs, votes, sign-ups, private lists), read its saved data or lists, or sign in, for example when an event is over or a form must stop taking entries. " +
				"Nothing is deleted: files, versions, saved data and lists are kept, and you can still update, read and export it. `offline: false` puts it back online at every address. Confirm with the person before taking a site offline.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"offline": map[string]any{"type": "boolean", "description": "true takes the site offline; false puts it back online."},
			}, "site", "offline"),
			// Changes what the public sees; nothing is deleted and the other
			// value undoes it.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				off, ok := args["offline"].(bool)
				if !ok {
					return output{}, errors.New("offline is required: true to take the site offline, false to put it back online")
				}
				body, _ := json.Marshal(map[string]bool{"offline": off})
				res := c.do(http.MethodPatch, "/v1/sites/"+url.PathEscape(name), body, nil)
				if !res.ok() {
					return output{}, restError("set_site_offline", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				out := map[string]any{"site": name, "offline": site.Offline, "url": site.liveURL()}
				text := name + " is back online at " + site.liveURL()
				if site.Offline {
					text = name + " is offline: " + site.liveURL() + " shows \"This site is offline\" and visitor saves are refused. Nothing was deleted."
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name:  "set_site_passcode",
			Title: "Put a passcode on a site",
			Description: "Put one passcode on a whole site (`action: set`): every address of it shows a plain \"This site is protected\" page until a visitor enters the passcode, and the unlock lasts until the passcode changes or everyone is signed out. " +
				"`read` shows whether the site has one and what it is; `remove` opens the site to everyone again; `sign_out_everyone` makes every visitor enter it again. " +
				"It is a shared passcode, not a login: anyone given it can open the site and pass it on, and it does not make saved data private per person. " +
				"ASK THE PERSON FIRST before setting, changing or removing a passcode, and use the passcode the person chose; if they ask you to pick one, choose a 6-digit code and tell them what it is. " +
				"A passcode typed in chat stays in the conversation. The owner's key and this connector keep working on a protected site.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"action":   map[string]any{"type": "string", "enum": []string{"set", "remove", "sign_out_everyone", "read"}, "description": "`set` puts (or changes) the passcode; `remove` takes it off; `sign_out_everyone` makes every visitor enter it again; `read` shows the current one."},
				"passcode": map[string]any{"type": "string", "description": "The passcode the person chose (required for `set`): at least 6 characters, any characters, digits only is fine."},
			}, "site", "action"),
			// Changes what the public sees; reversible (remove undoes it) and
			// nothing is deleted. read is a lookup, but one annotation covers
			// the tool, so it carries the write's.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				action, err := stringArg(args, "action")
				if err != nil {
					return output{}, errors.New("action is required: set, remove, sign_out_everyone or read")
				}
				path := "/v1/sites/" + url.PathEscape(name) + "/lock"
				var res upstreamResult
				switch action {
				case "set":
					raw, _ := args["passcode"].(string)
					if strings.TrimSpace(raw) == "" {
						return output{}, errors.New("passcode is required for action set: use the passcode the person chose")
					}
					body, _ := json.Marshal(map[string]string{"passcode": raw})
					res = c.do(http.MethodPut, path, body, nil)
				case "remove":
					res = c.do(http.MethodDelete, path, nil, nil)
				case "sign_out_everyone":
					res = c.do(http.MethodPost, path+"/sign-out-everyone", nil, nil)
				case "read":
					res = c.do(http.MethodGet, path, nil, nil)
				default:
					return output{}, errors.New("action must be set, remove, sign_out_everyone or read")
				}
				if !res.ok() {
					return output{}, restError("set_site_passcode", res)
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
				case action == "sign_out_everyone":
					text = "Everyone was signed out of " + name + "; visitors must enter the passcode again."
				case !lock.Protected:
					text = name + " has no passcode: anyone with its address can open it."
				case action == "set":
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
			},
		},
		{
			Name:  "keep_site",
			Title: "Keep a site up for good",
			Description: "Mark a site Keep (or clear the mark). Simple Host emails the owner about a site nobody has visited or updated for " + span(lim().IdleAfter) + " and moves it to Recently deleted " + span(lim().IdleGrace) + " later unless it is kept; a site marked Keep is never flagged. " +
				"Sites with their own domain or claimed name are never flagged either.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"keep": map[string]any{"type": "boolean", "description": "true (default) keeps the site up for good; false lets it be flagged again when idle."},
			}, "site"),
			// Changes a flag on the person's own site; reversible.
			Annotations: writes(false, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				keep := true
				if v, ok := args["keep"]; ok {
					b, isBool := v.(bool)
					if !isBool {
						return output{}, errors.New("keep must be true or false")
					}
					keep = b
				}
				body, _ := json.Marshal(map[string]bool{"keep": keep})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/keep", body, nil)
				if !res.ok() {
					return output{}, restError("keep_site", res)
				}
				text := name + " is marked Keep: it stays up even if nobody visits it."
				if !keep {
					text = name + " is no longer marked Keep: if nobody visits or updates it for " + span(lim().IdleAfter) + ", its owner is emailed before anything happens."
				}
				return output{Text: text, Structured: map[string]any{"site": name, "keep": keep}}, nil
			},
		},
		{
			Name:  "connect_domain",
			Title: "Connect a custom domain",
			Description: "Give a site a nicer address (optional: every site already has its own at https://<site>.<handle>.simple-host.app/). Either a free `<name>.simple-host.app` address (e.g. `clay-studio.simple-host.app`): active at once, no DNS step, first come first served. " +
				"Or the person's own domain (e.g. `rsvp.example.com` or `example.com`): returns the two DNS records they must add at their domain registrar, the address record (dns_record) and a TXT ownership record (ownership_record, to keep in place); relay both exactly, then check with domain_status until it is active. " +
				"Once active the site lives only at that address, its old address redirects there, and visitors sign in and save there. " +
				"Or `*.<their domain>` (e.g. `*.trips.example.com`, no `site` needed): an address family for the whole account, so every site X answers at X.trips.example.com once the wildcard DNS record and the TXT ownership record are seen and the operator's wildcard certificate is set up; relay the records and check with domain_status (domain `*.trips.example.com`). Ask before connecting one: it applies to every site of the account.",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc + " Not used for a `*.<domain>` address family."),
				"domain": str("The address without https://: a free `<name>.simple-host.app`, the person's own domain or subdomain, e.g. `rsvp.example.com`, or `*.<domain>` for an address family covering every site of the account."),
			}, "domain"),
			// Reaches an arbitrary outside domain and, once DNS proves it,
			// serves the site there. Nothing is deleted.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				domain, err := stringArg(args, "domain")
				if err != nil {
					return output{}, err
				}
				if suffix, ok := familyDomain(domain); ok {
					body, _ := json.Marshal(map[string]string{"suffix": suffix})
					res := c.do(http.MethodPost, "/v1/me/address-families", body, nil)
					if !res.ok() {
						return output{}, restError("connect_domain", res)
					}
					out := familySummary(res.body)
					return output{Text: jsonText(out), Structured: out}, nil
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"domain": domain})
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/domain", body, nil)
				if !res.ok() {
					return output{}, restError("connect_domain", res)
				}
				out := domainSummary(name, res.body)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "domain_status",
			Title: "Check a custom domain",
			Description: "Check whether a site's custom domain is connected: pending (the DNS records are not seen yet, or its certificate is being issued) or active. Shows the certificate's progress and, while pending, the earlier address the site is still served at. " +
				"With domain `*.<domain>`, checks the account's address family instead (no site needed).",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc + " Not used with a `*.<domain>` domain."),
				"domain": str("Optional: `*.<domain>` to check an address family of the account."),
			}),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				if d, _ := args["domain"].(string); strings.TrimSpace(d) != "" {
					if suffix, ok := familyDomain(d); ok {
						res := c.do(http.MethodGet, "/v1/me/address-families/"+url.PathEscape(suffix), nil, nil)
						if !res.ok() {
							return output{}, restError("domain_status", res)
						}
						out := familySummary(res.body)
						return output{Text: jsonText(out), Structured: out}, nil
					}
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/domain", nil, nil)
				if !res.ok() {
					return output{}, restError("domain_status", res)
				}
				out := domainSummary(name, res.body)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "remove_domain",
			Title: "Disconnect a domain",
			Description: "Disconnect a site's custom domain or free `<name>.simple-host.app` address. Only call this after the person has explicitly confirmed, in this conversation, that they want this specific address disconnected. " +
				"Pass the address being removed as `confirm_domain` (domain_status shows it). Afterwards the site is served at its own address again (or, if the removed domain was still pending, at the earlier address it was still using). " +
				"Links to a removed custom domain stop working; a removed free name keeps redirecting to the site and cannot be claimed by anyone else. " +
				"With confirm_domain `*.<domain>` it disconnects the account's address family (no site needed): every site stops answering under it and goes back to its own address.",
			InputSchema: object(map[string]any{
				"site":           str(siteDesc + " Not used for a `*.<domain>` address family."),
				"confirm_domain": str("The address being disconnected, typed out, as confirmation, e.g. `rsvp.example.com` or `*.trips.example.com`."),
			}, "confirm_domain"),
			// Re-addresses a public site, and a disconnected custom domain can
			// be claimed by someone else, so it is destructive; calling it
			// again would disconnect the next address, so not idempotent.
			Annotations: writes(true, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				confirm, err := stringArg(args, "confirm_domain")
				if err != nil {
					return output{}, err
				}
				if suffix, ok := familyDomain(confirm); ok {
					res := c.do(http.MethodDelete, "/v1/me/address-families/"+url.PathEscape(suffix), nil, nil)
					if !res.ok() {
						return output{}, restError("remove_domain", res)
					}
					out := map[string]any{"removed": "*." + suffix}
					return output{Text: "Disconnected the address family *." + suffix + ". Every site answers at its own address again.", Structured: out}, nil
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				path := "/v1/sites/" + url.PathEscape(name) + "/domain"
				res := c.do(http.MethodGet, path, nil, nil)
				if !res.ok() {
					return output{}, restError("remove_domain", res)
				}
				var cur struct {
					Domain *string `json:"domain"`
				}
				_ = json.Unmarshal(res.body, &cur)
				if cur.Domain == nil || *cur.Domain == "" {
					return output{}, fmt.Errorf("site %q has no domain connected; nothing was changed", name)
				}
				want := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(confirm)), "https://"), "http://"), "/")
				if want != strings.ToLower(*cur.Domain) {
					return output{}, fmt.Errorf("confirm_domain %q does not match the connected domain %q; nothing was changed", confirm, *cur.Domain)
				}
				// The server drops only this address: if the site's domain
				// changed since the read above, it answers domain_changed.
				res = c.do(http.MethodDelete, path+"?domain="+url.QueryEscape(*cur.Domain), nil, nil)
				if !res.ok() {
					return output{}, restError("remove_domain", res)
				}
				out := map[string]any{"site": name, "removed": *cur.Domain}
				if site, err := c.findSite(name); err == nil {
					out["url"] = site.liveURL()
				}
				text := "Disconnected " + *cur.Domain + " from " + name + "."
				if u, ok := out["url"].(string); ok && u != "" {
					text += " The site is now at " + u
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name:        "site_analytics",
			Title:       "How many people visited",
			Description: "Visits to a site over the last N days, split into people, bots and monitoring, with the most viewed pages and the domains visitors came from (people only). When asked how many people visited, report the `person` numbers only.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"days": map[string]any{"type": "integer", "description": "Window in days (1–90, default 30)."},
			}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				days, given, err := wholeNumber(args, "days", false, 1, 90)
				if err != nil {
					return output{}, err
				}
				if !given {
					days = 30
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics?days="+strconv.Itoa(days), nil, nil)
				if !res.ok() {
					return output{}, restError("site_analytics", res)
				}
				var full map[string]any
				_ = json.Unmarshal(res.body, &full)
				// Totals only: the daily and hourly series are for charts and
				// would swamp a conversation.
				out := map[string]any{"site": name, "range_days": full["range_days"], "totals": full["totals"], "last_24h": full["last_24h"]}
				// Top pages and referring domains are a second read; the
				// totals stand without them if it fails.
				if top := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics/top?days="+strconv.Itoa(days), nil, nil); top.ok() {
					var t struct {
						Pages     []any `json:"pages"`
						Referrers []any `json:"referrers"`
					}
					if json.Unmarshal(top.body, &t) == nil && t.Pages != nil && t.Referrers != nil {
						out["top_pages"], out["top_referrers"] = t.Pages, t.Referrers
					}
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "export_site",
			Title: "Download a copy of a site",
			Description: "Make a download link for a copy of one of the person's sites: a .tar.gz holding its live files and its saved data " +
				"(storage resources with their KV values, SQLite databases, and files, plus any older state and collections). The link works for " + span(lim().ExportLinkTTL) + " and only for that site; " +
				"give it to the person to click, and make a new one if it has expired. Do not post it anywhere public: until it expires, anyone with it can download the copy.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			// Not read-only: each call mints a new bearer download link on the server.
			Annotations: writes(false, false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/export-link", nil, nil)
				if !res.ok() {
					return output{}, restError("export_site", res)
				}
				var link struct {
					Site      string `json:"site"`
					URL       string `json:"url"`
					ExpiresAt string `json:"expires_at"`
				}
				_ = json.Unmarshal(res.body, &link)
				if link.URL == "" {
					return output{}, errors.New("export_site failed: the server returned no link; try again in a moment")
				}
				if link.Site == "" {
					link.Site = name
				}
				out := map[string]any{"site": link.Site, "url": link.URL, "expires_at": link.ExpiresAt}
				text := fmt.Sprintf("Download link for a copy of %s (files, saved state and collections), valid until %s: %s", link.Site, link.ExpiresAt, link.URL)
				return output{Text: text, Structured: out}, nil
			},
		},
	}
	tools = append(tools, storageTools()...)
	tools = append(tools, pageRecipeTool())
	schemas := outputSchemas()
	for i := range tools {
		if schema, ok := schemas[tools[i].Name]; ok {
			tools[i].OutputSchema = schema
		}
	}
	return tools
}

func filesSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Map of file path → text content, e.g. {\"index.html\": \"<!DOCTYPE html>…\", \"css/style.css\": \"body{…}\"}. Paths are relative, no leading slash, no `..`.",
		"additionalProperties": map[string]any{"type": "string"},
	}
}

func filesBase64Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps.",
		"additionalProperties": map[string]any{"type": "string"},
	}
}

// deploySite publishes the given files. mode is "create" (a new site only,
// never overwriting) or "replace" (an existing site only).
func deploySite(c *call, args map[string]any, mode string) (output, error) {
	tool := "create_site"
	if mode == "replace" {
		tool = "update_site"
	}
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	files, err := stringMap(args, "files")
	if err != nil {
		return output{}, err
	}
	if len(files) == 0 {
		return output{}, errors.New("files is required: an object mapping each path to its contents, including index.html")
	}
	binary, err := stringMap(args, "files_base64")
	if err != nil {
		return output{}, err
	}
	if _, ok := files["index.html"]; !ok {
		if _, ok := binary["index.html"]; !ok {
			return output{}, errors.New("files must include index.html at the site root")
		}
	}
	payload := map[string]any{"files": files}
	if len(binary) > 0 {
		payload["files_base64"] = binary
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(name) + "/files"
	publish := true
	if raw, present := args["publish"]; present && raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return output{}, errors.New("publish must be true or false")
		}
		publish = b
	}
	if !publish {
		if mode == "create" {
			return output{}, errors.New("create_site always makes a new site live; publish: false is for update_site")
		}
		path += "?publish=false"
	}

	var res upstreamResult
	if mode == "create" {
		res = c.do(http.MethodPost, path, body, nil)
	} else {
		res = c.do(http.MethodPut, path, body, nil)
	}
	if !res.ok() {
		_, refusalCode := upstreamMessage(res)
		switch {
		case res.status == http.StatusNotFound && mode == "replace":
			return output{}, fmt.Errorf("update_site failed: there is no site named %q in this account. Use create_site for a new site, or list_sites for existing names", name)
		case res.status == http.StatusConflict && mode == "create" && strings.Contains(string(res.body), `"recently_deleted":true`):
			return output{}, fmt.Errorf("create_site failed: a site named %q was deleted recently and is in Recently deleted, which keeps its name. Ask the person whether to bring it back with restore_site, or pick another name", name)
		case res.status == http.StatusConflict && mode == "create" && refusalCode == "site_exists":
			return output{}, fmt.Errorf("create_site failed: this account already has a site named %q, and create_site never overwrites. To change it, use update_site (read its files first); for a separate site, pick another name", name)
		}
		return output{}, restError(tool, res)
	}
	var site restSite
	_ = json.Unmarshal(res.body, &site)
	out := site.summary()
	out["file_count"] = len(files) + len(binary)
	if !publish {
		var stored struct {
			Version    int    `json:"unpublished_version"`
			PreviewURL string `json:"preview_url"`
		}
		_ = json.Unmarshal(res.body, &stored)
		out["unpublished_version"] = stored.Version
		text := fmt.Sprintf("Stored %s version %d without making it live; visitors still see version %d at %s.", name, stored.Version, site.ActiveVersion, site.liveURL())
		if stored.PreviewURL != "" {
			out["preview_url"] = stored.PreviewURL
			text += " Preview it (the link works for anyone who has it, for " + span(lim().PreviewLinkTTL) + "): " + stored.PreviewURL
		}
		text += fmt.Sprintf("\nWhen the person is happy, rollback_site with version %d makes it live.", stored.Version)
		return output{Text: text, Structured: out}, nil
	}
	verb := "Published a new version of"
	if mode == "create" {
		verb = "Created"
	}
	text := fmt.Sprintf("%s %s (version %d). Live at %s", verb, name, site.ActiveVersion, site.liveURL())
	return output{Text: text, Structured: out}, nil
}

const deploySizeTips = "Check who_am_i for account file usage and list_sites for site sizes before deploying. Hosted Simple Host keeps 4 versions; new sites have a 200 MB whole-file allowance and accounts 1 GB (operator exceptions apply). The separate live copy and every kept version count; saved data and storage resources do not. Compress photos before deploying: about 1600 px wide, WebP or JPEG at about 80% quality; phone photos are often 4–12 MB. Keep zip files, installers and videos elsewhere and link to them. Remove unused files; ask the person which old sites they no longer need."
