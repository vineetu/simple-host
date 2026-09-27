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
	"HANDLE_RENAME_EVERY_DAYS": {"accounts", "Once something is published, how often an account may change its handle.", false, true},
	"EMAIL_CHANGE_UNDO_DAYS":   {"accounts", "How long the undo link sent to the old address after a sign-in email change works.", true, true},

	"MAX_SITES_PER_ACCOUNT":    {"sites", "Sites one account may hold (sites in Recently deleted count).", false, true},
	"MAX_FILES_PER_SITE":       {"sites", "Files in one upload. It can only be lowered.", false, true},
	"PREVIEW_LINK_TTL_MINUTES": {"sites", "How long a preview link to a stored version works.", true, true},
	"EXPORT_LINK_TTL_MINUTES":  {"sites", "How long a site download link works (it holds private lists too).", true, true},

	"VISITOR_SESSION_DAYS":      {"accounts", "How long a visitor stays signed in on a site's own address, however active.", true, true},
	"VISITOR_SESSION_IDLE_DAYS": {"accounts", "How long a visitor sign-in lasts unused. Not longer than VISITOR_SESSION_DAYS.", true, true},

	"OAUTH_ACCESS_TTL_MINUTES": {"accounts", "How long an AI app's access token works before it refreshes.", true, true},
	"OAUTH_REFRESH_TTL_DAYS":   {"accounts", "How long an AI app stays connected without being used.", true, true},
	"OAUTH_UNUSED_CLIENT_DAYS": {"accounts", "An AI app registration that never connected, or went unused, is removed after this many days.", false, true},

	"DOMAIN_UNPROVEN_HOURS":          {"domains", "A connected domain whose DNS never points here is released after this long.", false, true},
	"DOMAIN_UNPROVEN_MAX_DAYS":       {"domains", "A domain that points here but never goes live over HTTPS is released after this long.", false, true},
	"DOMAIN_LAPSE_WARN_HOURS":        {"domains", "A live domain failing every check: its owner is emailed after this long.", false, true},
	"DOMAIN_LAPSE_HOURS":             {"domains", "... and it stops being the site's address after this long. Longer than DOMAIN_LAPSE_WARN_HOURS.", false, true},
	"DOMAIN_CHECK_INTERVAL_MINUTES":  {"domains", "How often connected domains are checked.", false, true},
	"DOMAIN_CERTS_PER_ACCOUNT_DAILY": {"domains", "New custom-domain certificates one account may ask for in a day.", false, true},
	"EVENT_TTL_DAYS":                 {"domains", "How long a claimed event hostname lives (only where EVENT_DNS_TOKEN is set).", false, false},
	"EVENT_MAX_CLAIMS":               {"domains", "Event hostnames one account may hold at once.", false, false},

	"DELETED_RETENTION_DAYS":     {"cleanup", "How long a deleted site stays restorable in Recently deleted.", false, true},
	"IDLE_AFTER_DAYS":            {"cleanup", "Idle cleanup: a site unused this long gets its owner a warning email.", false, true},
	"IDLE_GRACE_DAYS":            {"cleanup", "Idle cleanup: how long after the warning an unused site moves to Recently deleted.", false, true},
	"IDLE_REPLY_TO":              {"email", "Reply-To address of the idle-cleanup emails.", false, true},
	"ANALYTICS_RETENTION_DAYS":   {"cleanup", "How long visit analytics are kept.", false, true},
	"API_METRICS_RETENTION_DAYS": {"cleanup", "How long the admin page's API-call counts and shortened caller addresses are kept.", false, true},

	"AI_MAX_JOBS_PER_USER":   {"ai", "AI create: builds one person may run at once.", false, false},
	"AI_MAX_JOBS":            {"ai", "AI create: builds running at once on the whole server.", false, false},
	"AI_JOB_TIMEOUT_MINUTES": {"ai", "AI create: how long one build may run.", false, false},

	"RATE_LIMIT_SIGNIN_IP":         {"accounts", "Sign-in and email-change requests per address.", true, true},
	"RATE_LIMIT_SIGNIN_EMAIL":      {"accounts", "Sign-in codes sent to one email address.", true, true},
	"RATE_LIMIT_VISITOR_OAUTH":     {"accounts", "Visitor Google sign-ins per address.", true, true},
	"RATE_LIMIT_VISITOR_AUTH":      {"accounts", "Visitor email-code sign-ins per address.", true, true},
	"RATE_LIMIT_VISITOR":           {"accounts", "Finishing a visitor sign-in and signing out, per address.", true, true},
	"RATE_LIMIT_UPLOAD":            {"sites", "Uploads and deploys per client.", false, true},
	"RATE_LIMIT_STATE":             {"data", "Saved-data and list writes per client.", false, true},
	"RATE_LIMIT_SITE_OPS":          {"sites", "Deleting, changing and restoring sites, per address.", false, true},
	"RATE_LIMIT_EXPORT":            {"sites", "Site downloads per address.", false, true},
	"RATE_LIMIT_DOMAIN_CHECK":      {"domains", "\"Check again\" on a domain, per address.", false, true},
	"RATE_LIMIT_DOMAIN_CHECK_USER": {"domains", "\"Check again\" on a domain, per account.", false, true},
	"RATE_LIMIT_OAUTH_REGISTER":    {"accounts", "AI app registrations on the connector, per address.", true, true},
	"RATE_LIMIT_OAUTH_AUTHORIZE":   {"accounts", "AI app sign-in requests on the connector, per address.", true, true},
	"RATE_LIMIT_OAUTH_TOKEN":       {"accounts", "AI app token requests on the connector, per address.", true, true},
	"RATE_LIMIT_AI_IP":             {"ai", "AI create requests per address.", false, false},
	"RATE_LIMIT_AI_USER":           {"ai", "AI create requests per account.", false, false},
	"RATE_LIMIT_TRANSCRIBE":        {"ai", "Voice input requests, per address and per account.", false, false},

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
	"SAVED_DATA_DEFAULT_KIND":             {"data", "What a name no one declared is on a site made after the kinds. shared: anyone reads it and signed-in visitors save to it. declare_first: it takes no saves until the owner declares it. Older sites are shared either way.", true, true},

	"ASK_ENABLED":          {"ai", "Whether the \"Ask about this page\" box is shown (it also needs a model backend).", false, false},
	"ASK_BURST":            {"ai", "Ask: questions one address may ask at once.", false, false},
	"ASK_EVERY_SECONDS":    {"ai", "Ask: then one more question every this many seconds, per address.", false, false},
	"ASK_DAILY_MAX":        {"ai", "Ask: questions answered per day across everyone. 0 answers none.", false, false},
	"ASK_MAX_IN_FLIGHT":    {"ai", "Ask: questions answered at once on the whole server.", false, false},
	"ASK_MODEL":            {"ai", "Ask: the model the box asks, through the same backend as AI create.", false, false},
	"ASK_REASONING_EFFORT": {"ai", "Ask: how long the model thinks before answering. none answers in seconds.", false, false},
	"ASK_MAX_TOKENS":       {"ai", "Ask: longest answer, in tokens.", false, false},
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
		{Name: "SITE_HOSTS", Group: "server", Type: "enum", Default: "off", Allowed: []string{"off", "serve", "canonical"},
			Description: "Each site at its own address, <site>.<handle>.<SITE_DOMAIN>. Needs PERSON_HOSTS."},
		{Name: "SITE_CERT_DIR", Group: "domains", Type: "string",
			Description: "Where per-person certificates are requested from, and found ready from, the certificate issuer."},
		{Name: "DOMAIN_CERT_DIR", Group: "domains", Type: "string",
			Description: "Where custom-domain certificates are requested from the certificate issuer. Empty: issued by hand."},
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
			Description: "Accounts (comma-separated) whose sites the idle cleanup never touches."},

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
		case "email address", "model name":
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
