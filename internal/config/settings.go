package config

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The settings registry: every environment setting the server reads, with a
// plain description, its area, type, default and range. The operational
// times and limits come straight from Knobs() (default, range, unit); the
// rest are listed in otherSettings. `simple-host settings --json` prints it,
// docs/advanced/settings.json is that output (a test fails when they differ),
// and the setup helper at /setup and the docs/advanced tables are built from
// that file. Refresh with scripts/sync-settings.sh.

// SettingGroup is one area of the advanced docs (docs/advanced/<Page>).
type SettingGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Page string `json:"page"`
}

// Setting is one environment setting.
type Setting struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	// Type is int, duration, bool, enum, string, secret or rate.
	Type    string   `json:"type"`
	Default string   `json:"default"`
	Min     *int64   `json:"min,omitempty"`
	Max     *int64   `json:"max,omitempty"`
	Allowed []string `json:"allowed,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	// Loosest is the loosest value a security-sensitive rate limit accepts.
	Loosest string `json:"loosest,omitempty"`
	// StrictOrder lists a security-sensitive switch's or choice's allowed
	// values, strictest first. The setup helper's check never suggests a
	// value later in it than both the default and the visitor's own.
	StrictOrder []string `json:"strict_order,omitempty"`
	// ZeroIsNever: 0 turns a security-sensitive lifetime off (it then never
	// ends), so 0 is its loosest value, not its strictest.
	ZeroIsNever bool `json:"zero_is_never,omitempty"`
	// Security: loosening it, or leaking it, weakens sign-in or data safety.
	Security bool `json:"security_sensitive"`
	// Required: the server does not start without it.
	Required bool `json:"required"`
	// Basic: asked in the setup helper's basic mode.
	Basic bool `json:"basic"`
	// SmallBox: can be set on a Docker Compose box (compose.yaml passes it
	// through and install.sh keeps it on a re-run) and does something there.
	SmallBox bool `json:"small_box"`
	// InstallFlag is the install.sh flag that sets it, when there is one.
	InstallFlag string `json:"install_flag,omitempty"`
}

// SettingsDoc is the whole registry as `settings --json` prints it.
type SettingsDoc struct {
	Product    string         `json:"product"`
	RateFormat string         `json:"rate_format"`
	Groups     []SettingGroup `json:"groups"`
	Settings   []Setting      `json:"settings"`
}

// SettingGroups is every area, in the order the docs give them.
var SettingGroups = []SettingGroup{
	{"server", "Server and addresses", "server-and-addresses.md"},
	{"accounts", "Accounts and sign-in", "accounts-and-sign-in.md"},
	{"sites", "Sites and versions", "sites-and-versions.md"},
	{"data", "Saved data", "saved-data.md"},
	{"domains", "Domains and certificates", "domains-and-certificates.md"},
	{"events", "Hosted events", "hosted-events.md"},
	{"cleanup", "Cleanup and retention", "cleanup-and-retention.md"},
	{"email", "Email", "email.md"},
	{"ai", "AI features", "ai-features.md"},
	{"storage", "Storage and backups", "storage-and-backups.md"},
	{"observability", "Observability", "observability.md"},
}

// knobDoc is what Knobs() does not carry: the area and the sentence.
type knobDoc struct {
	group, desc string
	security    bool
	smallBox    bool
}

// knobDocs describes every knob in Knobs(). smallBox is false only where the
// feature cannot run on a Docker Compose box (it has no model backend and no
// event hostnames).
var knobDocs = map[string]knobDoc{
	"SIGNIN_CODE_TTL_MINUTES":  {"accounts", "How long an emailed sign-in code (and link) or email-change code works.", true, true},
	"MAX_KEYS_PER_ACCOUNT":     {"accounts", "API keys one account may create from the Keys panel.", false, true},
	"KEY_IDLE_EXPIRY_DAYS":     {"accounts", "An API key unused this long stops working (counted from its last use, or its creation). 0: keys work until revoked.", true, true},
	"HANDLE_RENAME_EVERY_DAYS": {"accounts", "Once something is published, how often an account may change its handle.", false, true},
	"EMAIL_CHANGE_UNDO_DAYS":   {"accounts", "How long the undo link sent to the old address after a sign-in email change works.", true, true},

	"MAX_SITES_PER_ACCOUNT":    {"sites", "Sites one account may hold (sites in Recently deleted count).", false, true},
	"MAX_SITES_OVERRIDES":      {"sites", "Accounts that may hold a different number of sites than MAX_SITES_PER_ACCOUNT: comma-separated <handle>:<sites>, e.g. chhotabreak:2000 (1 to 100000 each). It follows the handle: an account that changes its handle keeps its override under the old one.", false, true},
	"MAX_ARCHIVE_MB_OVERRIDES": {"sites", "Accounts whose sites may be a different size than MAX_ARCHIVE_MB: comma-separated <handle>:<MB>, e.g. jot-transcribe:300 (1 to 500 each). It applies to new deploys only (a larger site already live stays up) and follows the handle like MAX_SITES_OVERRIDES. A proxy in front must accept bodies this large (4/3 of it for JSON and connector deploys), and the connector takes messages that large from every account, so keep values modest.", false, true},
	"MAX_FILES_PER_SITE":       {"sites", "Files in one upload. It can only be lowered.", false, true},
	"PREVIEW_LINK_TTL_MINUTES": {"sites", "How long a preview link to a stored version works.", true, true},
	"EXPORT_LINK_TTL_MINUTES":  {"sites", "How long a site download link works (it holds private lists too).", true, true},

	"SITE_PASSCODES":                {"sites", "on lets owners put a passcode on a site (it needs PASSCODE_ENC_KEY and a per-site address). off refuses new ones; sites that already have one keep asking for it.", false, false},
	"PASSCODE_MIN_LENGTH":           {"sites", "Shortest passcode an owner may set, in characters. Any characters count; digits only is fine.", false, false},
	"PASSCODE_LOCKOUT_MINUTES":      {"sites", "How long one address is refused on a site once it has used up RATE_LIMIT_PASSCODE_IP.", false, false},
	"PASSCODE_SITE_LOCKOUT_MINUTES": {"sites", "How long a site refuses every passcode try once RATE_LIMIT_PASSCODE_SITE is used up (visitors already let in are not affected).", false, false},
	"RATE_LIMIT_PASSCODE_IP":        {"sites", "Wrong passcode tries on one site per address.", true, false},
	"RATE_LIMIT_PASSCODE_SITE":      {"sites", "Wrong passcode tries on one site from everyone together.", true, false},

	"VISITOR_SESSION_DAYS":      {"accounts", "How long a visitor stays signed in on a site's own address, however active.", true, true},
	"VISITOR_SESSION_IDLE_DAYS": {"accounts", "How long a visitor sign-in lasts unused. Not longer than VISITOR_SESSION_DAYS.", true, true},

	"OAUTH_ACCESS_TTL_MINUTES": {"accounts", "How long an AI app's access token works before it refreshes.", true, true},
	"OAUTH_REFRESH_TTL_DAYS":   {"accounts", "How long an AI app stays connected without being used.", true, true},
	"OAUTH_UNUSED_CLIENT_DAYS": {"accounts", "An AI app registration that never connected, or went unused, is removed after this many days.", false, true},

	"DOMAIN_UNPROVEN_HOURS":                  {"domains", "A connected domain whose DNS never points here is released after this long.", false, true},
	"DOMAIN_UNPROVEN_MAX_DAYS":               {"domains", "A domain that points here but never goes live over HTTPS is released after this long.", false, true},
	"DOMAIN_LAPSE_WARN_HOURS":                {"domains", "A live domain failing every check: its owner is emailed after this long.", false, true},
	"DOMAIN_LAPSE_HOURS":                     {"domains", "... and it stops being the site's address after this long. Longer than DOMAIN_LAPSE_WARN_HOURS.", false, true},
	"DOMAIN_CHECK_INTERVAL_MINUTES":          {"domains", "How often connected domains are checked.", false, true},
	"DOMAIN_CERTS_PER_ACCOUNT_DAILY":         {"domains", "New custom-domain certificates one account may ask for in a day.", false, true},
	"ADDRESS_FAMILIES":                       {"domains", "on lets an account connect *.<its domain> once, so every site of the account answers at <site>.<its domain> (an address family). Families also need ADDRESS_FAMILY_CERT_DIR and a wildcard certificate the operator sets up.", false, false},
	"ADDRESS_FAMILIES_PER_ACCOUNT":           {"domains", "Address families one account may connect. 0: none.", false, false},
	"ADDRESS_FAMILY_UNPROVEN_HOURS":          {"domains", "How long a new address family may wait for its DNS records before it is dropped.", false, false},
	"ADDRESS_FAMILY_LAPSE_WARN_HOURS":        {"domains", "How long a working address family may fail its checks before its owner is emailed.", false, false},
	"ADDRESS_FAMILY_LAPSE_HOURS":             {"domains", "How long a working address family may fail its checks before it is disconnected. Must be longer than ADDRESS_FAMILY_LAPSE_WARN_HOURS.", false, false},
	"ADDRESS_FAMILY_CHECK_INTERVAL_MINUTES":  {"domains", "How often address families are checked.", false, false},
	"ADDRESS_FAMILY_ACTIVE_RECHECK_MINUTES":  {"domains", "How often a working address family's DNS records are proved again.", false, false},
	"ADDRESS_FAMILY_CERTS_PER_ACCOUNT_DAILY": {"domains", "Per-site-name certificates one account's families may ask for in a day (for a later release; wildcard families need none).", false, false},
	"ADDRESS_FAMILY_RESERVED_LABELS":         {"domains", "Names (comma-separated) that never name a site under an address family.", false, false},
	"ADDRESS_FAMILY_CACHE_SECONDS":           {"domains", "How long the server keeps its list of working address families before reading it again.", false, false},
	"IDLE_EXEMPT_FAMILY_SITES":               {"cleanup", "on: a site whose main address is an address family is never removed as idle, like one with its own domain.", false, false},
	"EVENT_TTL_DAYS":                         {"domains", "How long a claimed event hostname lives (only where EVENT_DNS_TOKEN is set).", false, false},
	"EVENT_MAX_CLAIMS":                       {"domains", "Event hostnames one account may hold at once.", false, false},
	"EVENT_CREATE_PER_DAY":                   {"events", "Hosted events (EVENTS=hosted): events one account may create in a rolling day.", false, false},
	"EVENT_MAX_ACTIVE_PER_ORGANISER":         {"events", "Hosted events: events one account may run at once (every stage but archived).", false, false},
	"EVENT_TEAM_SIZE_DEFAULT":                {"events", "Hosted events: the team size cap a new event starts with; the organiser changes it.", false, false},
	"EVENT_SITES_KEEP_DAYS":                  {"events", "Hosted events: how long team sites stay up after an event closes, before they are removed (the organiser is warned by email first). The event page and results stay.", false, false},
	"HACK_INSTANCE_BUDGET_GB":                {"events", "Hosted events: disk the whole instance may use for team sites; new events are refused above 80% of it. 0: no budget.", false, false},

	"DELETED_RETENTION_DAYS":           {"cleanup", "How long a deleted site stays restorable in Recently deleted.", false, true},
	"IDLE_AFTER_DAYS":                  {"cleanup", "Idle cleanup: a site unused this long gets its owner a warning email.", false, true},
	"IDLE_GRACE_DAYS":                  {"cleanup", "Idle cleanup: how long after the warning an unused site moves to Recently deleted.", false, true},
	"IDLE_REPLY_TO":                    {"email", "Reply-To address of the idle-cleanup emails.", false, true},
	"ANALYTICS_RETENTION_DAYS":         {"cleanup", "How long visit analytics are kept.", false, true},
	"ANALYTICS_PAGES_PER_SITE_DAY":     {"observability", "Distinct pages a site's Top pages keeps per day; views of further new pages that day are counted together as (other).", false, true},
	"ANALYTICS_REFERRERS_PER_SITE_DAY": {"observability", "Distinct referring domains a site keeps per day; further new domains that day are counted together as (other).", false, true},
	"API_METRICS_RETENTION_DAYS":       {"cleanup", "How long the admin page's API-call counts and shortened caller addresses are kept.", false, true},
	"NETWORK_MONTHLY_ALLOWANCE_GB":     {"observability", "The box's free outbound data a month, in GB (1 TB = 1024 GB), which the admin page's network bar measures this month's outbound against. 10240 is Oracle Cloud Always Free's 10 TB. 0 hides the bar.", false, false},
	"NETWORK_ALERT_PCT":                {"observability", "The share of NETWORK_MONTHLY_ALLOWANCE_GB at which the admin page's network bar turns amber, and the box's daily watch script sends its one alert of the month.", false, false},
	"NETWORK_SAMPLE_MINUTES":           {"observability", "How often the box's network counters are added to the month's totals. The admin page adds the growth since the last sample itself, so this only bounds what a reboot can lose.", false, false},
	"API_GROWTH_RETENTION_DAYS":        {"cleanup", "How long the admin page's API growth counts (calls per day by country and by kind, no addresses) are kept. Keep at least 183 for the 6-month view.", false, true},
	"API_METRICS_FLUSH_SECONDS":        {"observability", "How often counted API calls are written to the database for the admin page's API traffic and growth. Calls counted since the last write are lost if the server stops abruptly.", false, true},

	"AI_MAX_JOBS_PER_USER":   {"ai", "AI create: builds one person may run at once.", false, false},
	"AI_MAX_JOBS":            {"ai", "AI create: builds running at once on the whole server.", false, false},
	"AI_JOB_TIMEOUT_MINUTES": {"ai", "AI create: how long one build may run.", false, false},

	"RATE_LIMIT_SIGNIN_IP":                 {"accounts", "Sign-in and email-change requests per address.", true, true},
	"RATE_LIMIT_SIGNIN_EMAIL":              {"accounts", "Sign-in codes sent to one email address.", true, true},
	"RATE_LIMIT_VISITOR_OAUTH":             {"accounts", "Visitor Google sign-ins per address.", true, true},
	"RATE_LIMIT_VISITOR_AUTH":              {"accounts", "Visitor email-code sign-ins per address.", true, true},
	"RATE_LIMIT_VISITOR":                   {"accounts", "Finishing a visitor sign-in and signing out, per address.", true, true},
	"RATE_LIMIT_UPLOAD":                    {"sites", "Uploads and deploys per client.", false, true},
	"RATE_LIMIT_STATE":                     {"data", "Saved-data and list writes per client.", false, true},
	"RATE_LIMIT_SITE_OPS":                  {"sites", "Deleting, changing and restoring sites, and admin sign-in tries, per address.", false, true},
	"RATE_LIMIT_EXPORT":                    {"sites", "Site and account downloads per address.", false, true},
	"RATE_LIMIT_ANALYTICS":                 {"sites", "Top pages and referring domains reads, per address.", false, true},
	"RATE_LIMIT_TLS_ASK":                   {"domains", "Certificate checks (/internal/tls-ask), per address.", false, true},
	"RATE_LIMIT_DOMAIN_CHECK":              {"domains", "\"Check again\" on a domain, per address.", false, true},
	"RATE_LIMIT_DOMAIN_CHECK_USER":         {"domains", "\"Check again\" on a domain, per account.", false, true},
	"RATE_LIMIT_ADDRESS_FAMILY_CHECK":      {"domains", "\"Check again\" on an address family, per address.", false, false},
	"RATE_LIMIT_ADDRESS_FAMILY_CHECK_USER": {"domains", "\"Check again\" on an address family, per account.", false, false},
	"RATE_LIMIT_HANDLE_CHECK":              {"accounts", "Address availability checks while someone types a handle (GET /v1/handles/check), per address.", false, true},
	"RATE_LIMIT_EVENT_CODES_IP":            {"events", "Hosted events: join, judge and team-code lookups and joins, per network address (roomy: a whole venue can share one address).", false, false},
	"RATE_LIMIT_EVENT_CODES_USER":          {"events", "Hosted events: join, judge and team-code joins and lookups, per account.", false, false},
	"RATE_LIMIT_EVENT_NAMES_USER":          {"events", "Hosted events: event address checks while someone types one, per account.", false, false},
	"RATE_LIMIT_OAUTH_REGISTER":            {"accounts", "AI app registrations on the connector, per address.", true, true},
	"RATE_LIMIT_OAUTH_AUTHORIZE":           {"accounts", "AI app sign-in requests on the connector, per address.", true, true},
	"RATE_LIMIT_OAUTH_TOKEN":               {"accounts", "AI app token requests on the connector, per address.", true, true},
	"RATE_LIMIT_AI_IP":                     {"ai", "AI create requests per address.", false, false},
	"RATE_LIMIT_AI_USER":                   {"ai", "AI create requests per account.", false, false},
	"RATE_LIMIT_TRANSCRIBE":                {"ai", "Voice input requests, per address and per account.", false, false},

	"SAVED_DATA_UNDO_DAYS":                {"data", "How long every change to saved data, and every deleted list item, can be restored.", false, true},
	"SAVED_DATA_HISTORY_MAX_MB":           {"data", "A site's saved-data history above this is thinned, oldest first.", false, true},
	"SAVED_DATA_SITE_MAX_MB":              {"data", "Largest a site's live saved data may grow.", false, true},
	"SAVED_DATA_SNAPSHOT_EVERY":           {"data", "History keeps a full copy of a site's data at least this often.", false, true},
	"SAVED_DATA_SWEEP_MINUTES":            {"data", "How often expired history and deleted items are removed.", false, true},
	"SAVED_DATA_WATCH_DAYS":               {"data", "The window the admin page's saved-data watch reports.", false, true},
	"SAVED_DATA_WATCH_INC_MAX":            {"data", "A visitor increment larger than this counts as large in the watch.", false, true},
	"SAVED_DATA_WATCH_ITEM_KB":            {"data", "A list item larger than this counts as large in the watch.", false, true},
	"SAVED_DATA_WATCH_KEEP_DAYS":          {"data", "Watch counts older than this are removed.", false, true},
	"SAVED_DATA_IDEMPOTENCY_HOURS":        {"data", "How long a retried write gets its first answer back instead of applying twice.", false, true},
	"SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE": {"data", "Retry keys remembered per site; the oldest past this are dropped.", false, true},
	"SAVED_DATA_READ_PER_SEC":             {"data", "Saved-data reads per second per site and address.", false, true},
	"SAVED_DATA_READ_BURST":               {"data", "Saved-data reads allowed at once above that rate.", false, true},
	"SAVED_DATA_APPEND_PER_MIN":           {"data", "List items one address may add per minute without the owner's key.", false, true},
	"SAVED_DATA_APPEND_BURST":             {"data", "List items allowed at once above that rate.", false, true},
	"SAVED_DATA_CONTENT_MAX_KB":           {"data", "Largest one Page info document may be.", false, true},
	"SAVED_DATA_CONTENT_NAMES_MAX":        {"data", "Page info names one site may declare.", false, true},
	"SAVED_DATA_ENTRY_MAX_KB":             {"data", "Largest one new Submissions entry may be.", false, true},
	"SAVED_DATA_ENTRIES_MAX":              {"data", "Live entries one Submissions name may hold.", false, true},
	"SAVED_DATA_WITHDRAW_UNDO_MINUTES":    {"data", "How long a visitor can bring back an entry they withdrew.", false, true},
	"SAVED_DATA_NOTIFY_EACH_MINUTES":      {"data", "\"Email me: each\" sends at most one email per name this often, listing what arrived.", false, true},
	"SAVED_DATA_NOTIFY_DAILY_HOURS":       {"data", "\"Email me: daily\" sends at most one digest per name this often.", false, true},
	"SAVED_DATA_SAVERS_MAX":               {"data", "Emails and domains in one site's who-may-save and block lists together.", false, true},
	"SAVED_DATA_ENTRIES_NAMES_MAX":        {"data", "Submissions names one site may declare.", false, true},
	"SAVED_DATA_PERSONAL_MAX_KB":          {"data", "Largest one person's record in one Personal name may be.", false, true},
	"SAVED_DATA_PERSONAL_NAMES_MAX":       {"data", "Personal names one site may declare.", false, true},
	"SAVED_DATA_PERSONAL_PEOPLE_MAX":      {"data", "People who may hold a record in one Personal name.", false, true},
	"SAVED_DATA_BOARD_ITEM_MAX_KB":        {"data", "Largest one Shared board item may be.", false, true},
	"SAVED_DATA_BOARD_MAX":                {"data", "Live items one Shared board may hold.", false, true},
	"SAVED_DATA_BOARD_NAMES_MAX":          {"data", "Shared board names one site may declare.", false, true},
	"SAVED_DATA_BOARD_WRITES_PER_MIN":     {"data", "Shared board adds, changes and deletes one signed-in person may make per minute, on top of the per-address rate.", false, true},
	"SAVED_DATA_DEFAULT_KIND":             {"data", "What a name no one declared is on a site made after the kinds. shared: anyone reads it and signed-in visitors save to it. declare_first: it takes no saves until the owner declares it. Older sites are shared either way.", true, true},

	"ASK_ENABLED":                    {"ai", "Whether the \"Ask about this page\" box is shown (it also needs a model backend).", false, false},
	"ASK_BURST":                      {"ai", "Ask: questions one address may ask at once.", false, false},
	"ASK_EVERY_SECONDS":              {"ai", "Ask: then one more question every this many seconds, per address.", false, false},
	"ASK_DAILY_MAX":                  {"ai", "Ask: questions answered per day across everyone. 0 answers none.", false, false},
	"ASK_MAX_IN_FLIGHT":              {"ai", "Ask: questions answered at once on the whole server.", false, false},
	"ASK_MODEL":                      {"ai", "Ask: the model the box asks, through the same backend as AI create.", false, false},
	"ASK_REASONING_EFFORT":           {"ai", "Ask: how long the model thinks before answering. none answers in seconds.", false, false},
	"ASK_MAX_TOKENS":                 {"ai", "Ask: longest answer, in tokens.", false, false},
	"SETUP_CHECK_DAILY_MAX":          {"ai", "The setup helper's optional \"Check my choices\": checks answered per day across everyone, through the Ask model. 0 turns it off.", false, false},
	"SETUP_CHECK_MAX_IN_FLIGHT":      {"ai", "The setup helper's check: checks answered at once on the whole server, apart from Ask's own.", false, false},
	"SETUP_CHECK_PER_NETWORK_DAILY":  {"ai", "The setup helper's check: checks one network (a /24, or a /48 for IPv6) may run per day.", false, false},
	"SETUP_ASSIST_DAILY_MAX":         {"ai", "The setup helper's assistant: messages answered per day across everyone, through the Ask model. 0 turns it off and hides its panel.", false, false},
	"SETUP_ASSIST_MAX_IN_FLIGHT":     {"ai", "The setup helper's assistant: messages answered at once on the whole server, apart from Ask's and the check's.", false, false},
	"SETUP_ASSIST_PER_NETWORK_DAILY": {"ai", "The setup helper's assistant: messages one network (a /24, or a /48 for IPv6) may send per day.", false, false},
}

// strictOrder is StrictOrder for the security-sensitive switches and choices
// (a test fails when one is missing or does not list exactly Allowed).
var strictOrder = map[string][]string{
	"WRITE_AUTH_MODE":         {"on", "log", "off"},
	"SAVED_DATA_DEFAULT_KIND": {"declare_first", "shared"},
}

// zeroIsNever names the security-sensitive lifetimes where 0 means never:
// shorter is stricter, and 0 is looser than any other value.
var zeroIsNever = map[string]bool{
	"KEY_IDLE_EXPIRY_DAYS": true,
}

// otherSettings are the settings outside Knobs(): where the server lives,
// credentials, sign-in providers, email, the model backend and the switches
// that were settings before the knobs existed.
func otherSettings() []Setting {
	return []Setting{
		{Name: "SITE_DOMAIN", Group: "server", Type: "string", Basic: true, SmallBox: true, InstallFlag: "--host",
			Description: "The domain this server lives at, like hack.example.com. Unset, the server starts in setup mode and asks for one."},
		{Name: "CONTENT_HOST", Group: "server", Type: "string", Default: "sites.<SITE_DOMAIN>", SmallBox: true, InstallFlag: "--content",
			Description: "The separate hostname sites are served from, so pages never share an origin with the dashboard."},
		{Name: "PUBLIC_BASE_URL", Group: "server", Type: "string", Default: defaultPublicBaseURL,
			Description: "The server's own address, used in emails and sign-in redirects. install.sh sets it to https://<SITE_DOMAIN>."},
		{Name: "ADMIN_API_KEY", Group: "accounts", Type: "secret", Required: true, Security: true,
			Description: "The admin's key. install.sh generates it and shows it once."},
		{Name: "DB_DSN", Group: "storage", Type: "secret", Required: true, Security: true,
			Description: "The Postgres connection string. install.sh sets it for the bundled database."},
		{Name: "DATA_DIR", Group: "storage", Type: "string", Default: defaultDataDir,
			Description: "The folder that holds every site's files and versions."},
		{Name: "PORT", Group: "server", Type: "int", Default: defaultPort, Min: i64(1), Max: i64(65535),
			Description: "The port the server listens on."},
		{Name: "BIND_ADDR", Group: "server", Type: "string",
			Description: "The interface the server listens on, like 127.0.0.1 behind a proxy. Empty listens on all."},
		{Name: "DEPLOY_SCRIPT", Group: "server", Type: "string",
			Description: "A script run after each site goes live. Empty runs nothing."},
		{Name: "CNAME_TARGET", Group: "domains", Type: "string", Default: "cname.<SITE_DOMAIN>",
			Description: "The hostname people point their own domain at with a CNAME record."},
		{Name: "CUSTOM_DOMAIN_IP", Group: "domains", Type: "string",
			Description: "This server's public IPv4, given as the A record for a bare domain (brand.com)."},
		{Name: "PERSON_HOSTS", Group: "server", Type: "enum", Default: "off", Allowed: []string{"off", "serve", "canonical"},
			Description: "Each account at its own address, <handle>.<SITE_DOMAIN>. canonical makes it the address handed out."},
		{Name: "EVENTS", Group: "events", Type: "enum", Default: "off", Allowed: []string{"off", "hosted"},
			Description: "hosted runs this server as a hosted hackathon platform (simple-hack.app): anyone signed in creates an event with its own page at <event>.<SITE_DOMAIN>, and accounts own no personal sites."},
		{Name: "EVENT_NAME_PEER", Group: "events", Type: "string",
			Description: "The loopback address (http://127.0.0.1:<port>) of the other server handing out names under the same zone; each asks the other before taking a name. Used with EVENTS=hosted, or with EVENT_DNS_TOKEN (self-host claims); ignored otherwise."},
		{Name: "SITE_HOSTS", Group: "server", Type: "enum", Default: "off", Allowed: []string{"off", "serve", "canonical"},
			Description: "Each site at its own address, <site>.<handle>.<SITE_DOMAIN>. Needs PERSON_HOSTS."},
		{Name: "SITE_CERT_DIR", Group: "domains", Type: "string",
			Description: "Where per-person certificates are requested from, and found ready from, the certificate issuer."},
		{Name: "SITE_BASE_DOMAIN", Group: "server", Type: "string", Default: "<SITE_DOMAIN>",
			Description: "The domain people's and sites' addresses live under, when not SITE_DOMAIN itself. The app stays on SITE_DOMAIN."},
		{Name: "SITE_BASE_MOVE", Group: "server", Type: "enum", Default: "off", Allowed: []string{"off", "serve", "canonical", "redirect", "permanent"},
			Description: "How far addresses have moved from SITE_DOMAIN to SITE_BASE_DOMAIN: both answer (serve), the new ones are handed out (canonical), old ones redirect (redirect: 302, permanent: 301)."},
		{Name: "SITE_BASE_CERT_DIR", Group: "domains", Type: "string",
			Description: "Like SITE_CERT_DIR, for the per-person certificates under SITE_BASE_DOMAIN. Required, with ready/ and requests/ in it, once SITE_BASE_MOVE is on with its own domain: the server will not start without it."},
		{Name: "DOMAIN_CERT_DIR", Group: "domains", Type: "string",
			Description: "Where custom-domain certificates are requested from the certificate issuer. Empty: issued by hand."},
		{Name: "ADDRESS_FAMILY_CERT_DIR", Group: "domains", Type: "string",
			Description: "Where address-family requests go to the family issuer, and where it says a family is served (ready/). Empty: no address family is ever served."},
		{Name: "SETUP_PASSWORD", Group: "server", Type: "secret", Security: true,
			Description: "The password a box in setup mode asks for. install.sh generates it."},
		{Name: "SETUP_PUBLIC_API", Group: "server", Type: "string", Default: "https://" + canonicalPublicHost,
			Description: "Where a box in setup mode claims a free hostname from."},
		{Name: "OPENAI_APPS_CHALLENGE", Group: "server", Type: "string",
			Description: "The OpenAI plugin portal's domain-verification token. Unset: not served."},

		{Name: "GOOGLE_OAUTH_CLIENT_ID", Group: "accounts", Type: "string", Basic: true, SmallBox: true,
			Description: "Google sign-in for owners and visitors: the OAuth client ID. Set with the secret, or neither."},
		{Name: "GOOGLE_OAUTH_CLIENT_SECRET", Group: "accounts", Type: "secret", Security: true, Basic: true, SmallBox: true,
			Description: "Google sign-in: the OAuth client secret."},
		{Name: "GITHUB_OAUTH_CLIENT_ID", Group: "accounts", Type: "string",
			Description: "An optional second visitor sign-in provider: its OAuth client ID. Set with the secret, or neither."},
		{Name: "GITHUB_OAUTH_CLIENT_SECRET", Group: "accounts", Type: "secret", Security: true,
			Description: "The second provider's OAuth client secret."},
		{Name: "REVIEW_ACCOUNT_EMAIL", Group: "accounts", Type: "string",
			Description: "A plugin reviewer's account that may sign in with a password on the connector. Set with the hash, or neither."},
		{Name: "REVIEW_ACCOUNT_PASSWORD_HASH", Group: "accounts", Type: "secret", Security: true,
			Description: "That reviewer account's password hash."},

		{Name: "MAX_ARCHIVE_MB", Group: "sites", Type: "int", Default: "100", Min: i64(1), Unit: "MB", SmallBox: true, InstallFlag: "--max-site-mb",
			Description: "Largest upload, and the size one site may have."},
		{Name: "KEEP_VERSIONS", Group: "sites", Type: "int", Default: "0", Min: i64(0), Unit: "versions", SmallBox: true, InstallFlag: "--keep-versions",
			Description: "Deploys kept per site; 0 keeps all. install.sh sets 1, which means no rollback."},
		{Name: "PASSCODE_ENC_KEY", Group: "sites", Type: "secret", Security: true,
			Description: "Key that seals site passcodes (32 random bytes, base64: openssl rand -base64 32). Unset: no site can get a passcode. Changing it makes every stored passcode unreadable and signs every visitor out; owners then set new ones."},
		{Name: "PREVIEW_ACCOUNTS", Group: "sites", Type: "string",
			Description: "Accounts (comma-separated) whose sites expire on their own."},
		{Name: "PREVIEW_TTL_HOURS", Group: "sites", Type: "duration", Default: "48", Min: i64(1), Unit: "hours",
			Description: "How long those accounts' sites last."},

		{Name: "WRITE_AUTH_MODE", Group: "data", Type: "enum", Default: "log", Allowed: []string{"off", "log", "on"}, Security: true,
			Description: "on: page saves need a signed-in visitor or the owner's key. log: allowed, and logged."},

		{Name: "EVENT_DNS_TOKEN", Group: "domains", Type: "secret", Security: true,
			Description: "The DNS token that hands out event hostnames. The public instance only."},
		{Name: "EVENT_DNS_TEAM_ID", Group: "domains", Type: "string",
			Description: "The DNS account the event token belongs to."},
		{Name: "EVENT_DOMAINS", Group: "domains", Type: "string",
			Description: "Zones (comma-separated) event hostnames are handed out under."},

		{Name: "IDLE_CLEANUP", Group: "cleanup", Type: "bool", Default: "off", Allowed: []string{"on", "off"},
			Description: "on warns owners of long-unused sites, then moves them to Recently deleted."},
		{Name: "IDLE_CLEANUP_MAX_EMAILS", Group: "cleanup", Type: "int", Default: "50", Min: i64(1), Unit: "emails",
			Description: "Idle-cleanup emails one run sends."},
		{Name: "IDLE_CLEANUP_EXEMPT_HANDLES", Group: "cleanup", Type: "string",
			Description: "Accounts (comma-separated handles) whose sites the idle cleanup never touches. An account that later changes its handle stays exempt under the old one."},

		{Name: "RESEND_API_KEY", Group: "email", Type: "secret", Security: true, Basic: true, SmallBox: true,
			Description: "A Resend API key for sending email (sign-in codes, alerts). Without it, email sign-in is off."},
		{Name: "MAIL_FROM", Group: "email", Type: "string", Default: defaultMailFrom, Basic: true, SmallBox: true,
			Description: "The sender of every email, on a domain verified with Resend."},

		{Name: "LLM_PROVIDER", Group: "ai", Type: "enum", Default: defaultLLMProvider, Allowed: LLMProviderNames(),
			Description: "The model backend for AI create and Ask."},
		{Name: "LLM_API_KEY", Group: "ai", Type: "secret", Security: true,
			Description: "The model backend's key. Ask and AI create run only with a backend set."},
		{Name: "LLM_BASE_URL", Group: "ai", Type: "string",
			Description: "The backend's address; wins over the provider's."},
		{Name: "LLM_MODEL", Group: "ai", Type: "string",
			Description: "The model AI create uses; wins over the provider's."},
		{Name: "VISION_PROVIDER", Group: "ai", Type: "enum", Default: "<LLM_PROVIDER>", Allowed: LLMProviderNames(),
			Description: "AI create: the backend that reads attached images."},
		{Name: "VISION_API_KEY", Group: "ai", Type: "secret", Security: true,
			Description: "AI create: the image backend's key."},
		{Name: "VISION_BASE_URL", Group: "ai", Type: "string",
			Description: "AI create: the image backend's address."},
		{Name: "VISION_MODEL", Group: "ai", Type: "string",
			Description: "AI create: the image model."},
		{Name: "TRANSCRIBE_URL", Group: "ai", Type: "string",
			Description: "A speech-to-text service on this server for voice input. Unset: no microphone button."},
		{Name: "TRANSCRIBE_TICKET_SECRET", Group: "ai", Type: "secret", Security: true,
			Description: "Shared with the speech service for live transcription."},

		{Name: "ANALYTICS_LOG", Group: "observability", Type: "string",
			Description: "The web server's access log that visit analytics are read from. Empty: no analytics."},
		{Name: "ANALYTICS_SALT", Group: "observability", Type: "secret", Security: true,
			Description: "Salt for the hashed visitor addresses in analytics. Empty: derived from ADMIN_API_KEY."},
		{Name: "NETWORK_INTERFACE", Group: "observability", Type: "string", Default: "<default route>",
			Description: "The network interface whose traffic the admin page reports as the box's own. Empty: the one the default route leaves by. Inside a container, where that is the container's own, network use is off unless this is set."},
		{Name: "GEOIP_DIR", Group: "observability", Type: "string", Default: "<DATA_DIR>/../geoip",
			Description: "Where the local location databases are. Missing files mean blank locations, never a lookup elsewhere."},
	}
}

func i64(n int64) *int64 { return &n }

// Settings is every setting, grouped in SettingGroups order and, within a
// group, knobs in Knobs() order followed by the others.
func Settings() []Setting {
	def := DefaultLimits()
	var all []Setting
	for _, k := range Knobs() {
		d, ok := knobDocs[k.Env]
		if !ok {
			panic("config: knob " + k.Env + " has no entry in knobDocs (settings.go)")
		}
		s := Setting{Name: k.Env, Group: d.group, Description: d.desc, Default: k.Value(&def),
			Unit: k.Unit, Security: d.security || k.Sensitive, SmallBox: d.smallBox}
		switch k.Unit {
		case "burst,every":
			s.Type, s.Unit = "rate", ""
			if k.Sensitive {
				r, _ := parseRate(k.Env, s.Default)
				s.Loosest = Rate{r.Burst * secLoosen, r.Every / secLoosen}.String()
			}
		case "on/off":
			s.Type, s.Unit, s.Allowed = "bool", "", []string{"on", "off"}
		case "email address", "model name", "DNS labels", "handle:sites":
			s.Type = "string"
		case "seconds", "minutes", "hours", "days":
			s.Type = "duration"
		default:
			if strings.Contains(k.Unit, "/") {
				s.Type, s.Allowed, s.Unit = "enum", strings.Split(k.Unit, "/"), ""
			} else {
				s.Type = "int"
			}
		}
		if s.Type == "int" || s.Type == "duration" {
			s.Min, s.Max = i64(k.Min), i64(k.Max)
		}
		all = append(all, s)
	}
	all = append(all, otherSettings()...)
	for i := range all {
		all[i].StrictOrder = strictOrder[all[i].Name]
		all[i].ZeroIsNever = zeroIsNever[all[i].Name]
	}
	var out []Setting
	for _, g := range SettingGroups {
		for _, s := range all {
			if s.Group == g.ID {
				out = append(out, s)
			}
		}
	}
	if len(out) != len(all) {
		panic("config: a setting names a group that is not in SettingGroups")
	}
	return out
}

// SettingsJSON is the registry as docs/advanced/settings.json holds it.
func SettingsJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(SettingsDoc{Product: "small-box", RateFormat: "<burst>,<every>", Groups: SettingGroups, Settings: Settings()})
	return buf.Bytes(), err
}
