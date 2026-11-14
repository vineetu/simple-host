package main

// The union of registrations across all editions/configurations. These patterns
// are read from connector.go, ui.go, hack_ui.go and hack_get_started.go. Keep
// separate entries for separate call sites, including conditional alternatives.
var dynamicSites = dynamicTable{
	"internal/handler/connector.go:Register": {
		{`"GET " + museClientPath`, []string{"GET /.well-known/muse-client.json"}},
		{`m + " /mcp"`, []string{"POST /mcp", "GET /mcp", "DELETE /mcp"}},
	},
	"internal/handler/ui.go:RegisterUIRoutes": {
		{`"GET /" + name`, []string{"GET /llms.txt", "GET /install.html", "GET /auth.js", "GET /signin.js", "GET /features.json"}},
		{`"GET /" + name`, []string{"GET /openapi.yaml", "GET /openapi.json"}},
		{`"GET /" + name`, []string{"GET /favicon.svg", "GET /favicon.ico", "GET /favicon-32.png", "GET /apple-touch-icon.png"}},
		{`"GET /skills/" + dir + ".zip"`, skillPatterns(".zip", "host", "hack")},
		{`"GET /skills/" + dir + "/SKILL.md"`, skillPatterns("/SKILL.md", "host", "hack")},
		{`"GET /skills/" + dir`, skillPatterns("", "host", "hack")},
		{`"GET /skills/" + dir + "/references/{file}"`, skillPatterns("/references/{file}", "host", "hack")},
	},
	"products/hack/hackhttp/hack_ui.go:RegisterHackUI": {
		{`p`, []string{"GET /signin", "GET /account", "GET /events", "GET /events/new", "GET /e/{slug}/team", "GET /e/{slug}/manage", "GET /e/{slug}/apply", "GET /join/{code}", "GET /judge/{code}", "GET /organiser/{code}", "GET /team/{code}"}},
	},
	"products/hack/hackhttp/hack_get_started.go:RegisterHackGetStarted": {
		{`"GET /simple-hack-skills-only-" + version + ".zip"`, versionPatterns([]string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"})},
		{`"GET /simple-hack-skills-only-" + version + ".zip"`, versionPatterns([]string{"0.2.6", "0.2.7", "0.2.8", "0.2.9", "0.2.10", "0.2.11", "0.2.12", "0.2.13"})},
	},
}

var productSkills = map[string][]string{
	"host": {"connect-domain", "website-deploy", "website-deploy-builder"},
	"hack": {"join-hackathon", "judge-hackathon", "run-hackathon", "website-deploy", "website-deploy-builder"},
}

func skillPatterns(suffix string, products ...string) []string {
	var out []string
	for _, product := range products {
		for _, name := range productSkills[product] {
			out = append(out, "GET /skills/"+name+suffix)
		}
	}
	return uniqueStrings(out)
}
func versionPatterns(versions []string) []string {
	var out []string
	for _, version := range versions {
		out = append(out, "GET /simple-hack-skills-only-"+version+".zip")
	}
	return out
}
