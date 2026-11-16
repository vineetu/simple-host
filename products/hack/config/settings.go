package config

import platform "github.com/vsriram/simple-host/internal/config"

func i64(n int64) *int64 { return &n }
func Settings() []platform.Setting {
	all := []platform.Setting{
		{Name: "MCP_APP_HACK_JUDGE", Group: "server", Description: "Simple Hack judge views (queue, scoring, rubric, invite) in compatible MCP Apps clients. off, on, or accounts:alice,bob. Simple Hack event connections only, never the ChatGPT listing.", Type: "string", Default: "off", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "MCP_APP_HACK_ORGANISER", Group: "server", Description: "Simple Hack organiser views (overview and stage, people and applications, judging dashboard) in compatible MCP Apps clients. off, on, or accounts:alice,bob. Simple Hack event connections only, never the ChatGPT listing.", Type: "string", Default: "off", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_LIMIT_EXEMPT", Group: "events", Description: "Comma-separated sign-in emails and whole domains (@domain.org), case-insensitive with spaces trimmed; no subdomain matching. Empty by default. Admins and EVENT_LIMIT_EXEMPT accounts are never limited by EVENT_CREATE_PER_DAY (default 20) or EVENT_MAX_ACTIVE_PER_ORGANISER (no limit by default; 0 = no limit for either).", Type: "string", Default: "", Unit: "emails and domains", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_CREATE_PER_DAY", Group: "events", Description: "Hosted events (EVENTS=hosted): events one account may create in a rolling day (default 20; 0 = no limit). Admins and EVENT_LIMIT_EXEMPT accounts are never limited.", Type: "int", Default: "20", Min: i64(0), Max: i64(1000), Unit: "events", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_MAX_ACTIVE_PER_ORGANISER", Group: "events", Description: "Hosted events: events one account may run at once (every stage but archived); no limit by default (0 = no limit). Admins and EVENT_LIMIT_EXEMPT accounts are never limited.", Type: "int", Default: "0", Min: i64(0), Max: i64(1000), Unit: "events", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_APPLY_PENDING_PER_EVENT", Group: "events", Description: "Hosted events: applications one event may have waiting for a decision at once, apart from the per-account pending cap.", Type: "int", Default: "500", Min: i64(1), Max: i64(100000), Unit: "applications", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_TEAM_SIZE_DEFAULT", Group: "events", Description: "Hosted events: the team size cap a new event starts with; the organiser changes it.", Type: "int", Default: "20", Min: i64(1), Max: i64(50), Unit: "people", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_SITES_KEEP_DAYS", Group: "events", Description: "Hosted events: how long team sites stay up after an event closes, before they are removed (the organiser is warned by email first). The event page and results stay.", Type: "duration", Default: "30", Min: i64(1), Max: i64(3650), Unit: "days", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "EVENT_REMOVAL_WARN_DAYS", Group: "events", Description: "Hosted events: how long before team sites are removed the organiser is warned by email, once. The event page and results stay.", Type: "duration", Default: "14", Min: i64(1), Max: i64(3650), Unit: "days", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "HACK_INSTANCE_BUDGET_GB", Group: "events", Description: "Hosted events: disk the whole instance may use for team sites; new events are refused above 80% of it. 0: no budget.", Type: "int", Default: "10", Min: i64(0), Max: i64(100000), Unit: "GB", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "HACK_EVENT_ICON_MAX_BYTES", Group: "events", Description: "Hosted events: largest PNG, JPEG or WebP event icon upload in bytes.", Type: "int", Default: "131072", Min: i64(1), Max: i64(4194304), Unit: "bytes", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "RATE_LIMIT_EVENT_CODES_IP", Group: "events", Description: "Hosted events: join, judge and team-code lookups and joins, per network address (roomy: a whole venue can share one address).", Type: "rate", Default: "120,1s", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "RATE_LIMIT_EVENT_CODES_USER", Group: "events", Description: "Hosted events: join, judge and team-code joins and lookups, per account.", Type: "rate", Default: "20,3s", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "RATE_LIMIT_EVENT_NAMES_USER", Group: "events", Description: "Hosted events: event address checks while someone types one, per account.", Type: "rate", Default: "60,1s", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "RATE_LIMIT_EVENT_APPLY_IP", Group: "events", Description: "Hosted events: applications to an event without a join link, per network address.", Type: "rate", Default: "30,2m", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "RATE_LIMIT_EVENT_APPLY_USER", Group: "events", Description: "Hosted events: applications to an event without a join link, per account.", Type: "rate", Default: "5,12m", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "HACK_DEMO_EVENT_SLUG", Group: "events", Description: "Base event slug for the standing demo on the hosted hackathon platform; empty uses demo.", Type: "string", Default: "demo", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_BASE_URL", Group: "events", Description: "The OpenAI-compatible API root (e.g. an Ollama Cloud endpoint) the organiser assistant chat sends chat-completions requests to. Unset (with ASSISTANT_MODEL): the assistant is off and the UI shows nothing.", Type: "string", Default: "", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_MODEL", Group: "events", Description: "The model name sent to ASSISTANT_BASE_URL for the organiser assistant chat. Unset: the assistant is off.", Type: "string", Default: "", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_API_KEY", Group: "events", Description: "Bearer key for ASSISTANT_BASE_URL, when the endpoint needs one.", Type: "secret", Default: "", Security: true, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_REASONING_EFFORT", Group: "events", Description: "Sent as reasoning_effort when set; `none` turns thinking off on DeepSeek via Ollama Cloud. Leave empty for providers that reject it.", Type: "string", Default: "", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_MONTHLY_BUDGET_USD", Group: "events", Description: "Instance-wide monthly spend ceiling for the organiser assistant, in US dollars. Once reached, every event's assistant refuses new questions until the 1st of the next month.", Type: "string", Default: "5", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_DAILY_QUESTIONS", Group: "events", Description: "Questions one organiser may ask the assistant per UTC day, across every event they organise.", Type: "string", Default: "30", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_PRICE_INPUT_PER_MTOK_USD", Group: "events", Description: "The configured model's input price, in US dollars per million tokens, used to account spend against ASSISTANT_MONTHLY_BUDGET_USD.", Type: "string", Default: "0.5", Security: false, Required: false, Basic: false, SmallBox: false},
		{Name: "ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD", Group: "events", Description: "The configured model's output price, in US dollars per million tokens, used to account spend against ASSISTANT_MONTHLY_BUDGET_USD.", Type: "string", Default: "2.0", Security: false, Required: false, Basic: false, SmallBox: false},
	}
	def := DefaultLimits()
	for i := range all {
		for _, k := range Knobs() {
			if all[i].Name == k.Env {
				all[i].Default = k.Value(&def)
				if all[i].Min != nil {
					all[i].Min = i64(k.Min)
					all[i].Max = i64(k.Max)
				}
			}
		}
	}
	// Preserve the product's descriptions of settings that the platform also reads.
	for _, setting := range platform.Settings() {
		switch setting.Name {
		case "SITE_DOMAIN":
			all = append(all, setting)
		case "TEAM_SITE_FLAT_HOSTS":
			all = append(all, setting)
		case "SITE_STORAGE_FILES_MAX_BYTES":
			all = append(all, setting)
		case "SUPPORT_EMAIL":
			all = append(all, setting)
		case "SITE_STORAGE_MAX_BYTES":
			all = append(all, setting)
		case "EVENTS":
			all = append(all, setting)
		}
	}
	for i := range all {
		switch all[i].Name {
		case "SITE_DOMAIN":
			all[i].Description = "The domain this server lives at, like hack.example.com. Unset, the server starts in setup mode and asks for one."
		case "EVENTS":
			all[i].Description = "hosted runs this server as a hosted hackathon platform (simple-hack.app): anyone signed in creates an event with its own page at <event>.<SITE_DOMAIN>, and accounts own no personal sites."
		case "TEAM_SITE_FLAT_HOSTS":
			all[i].Description = "Hosted events: true serves a team's site at <team>--<event>.<SITE_DOMAIN> (one DNS label) instead of <team>.<event>.<SITE_DOMAIN> (two), so one wildcard certificate and one ingress rule cover every event and team instead of a certificate SAN and a Helm upgrade per event. Hosted simple-hack.app keeps this off: behaviour is byte-for-byte unchanged."
		case "SUPPORT_EMAIL":
			all[i].Description = "This install's own support contact, shown on its pages, in its emails and in error messages instead of ours. Unset: IDLE_REPLY_TO if that is set, else \"whoever runs this server\" on a self-hosted install (unchanged on simple-host.app/simple-hack.app)."
		case "SITE_STORAGE_MAX_BYTES":
			all[i].Description = "Durable byte budget per site for KV and SQLite (Hack defaults to 1000000, pooled with files); SQLite WAL and backups need additional temporary disk."
		case "SITE_STORAGE_FILES_MAX_BYTES":
			all[i].Description = "Separate raw-file allowance per website on Simple Host; Hack retains SITE_STORAGE_MAX_BYTES pooled allowance."
		}
	}
	return all
}
