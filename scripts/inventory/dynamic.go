package main

// The union of registrations across all editions/configurations. These patterns
// are read from connector.go, ui.go, hack_ui.go, hack_get_started.go,
// hack_film.go and the product's asset_files.go. Keep
// separate entries for separate call sites, including conditional alternatives.
var dynamicSites = dynamicTable{
	"host/server/build.go:Build": {
		{`pattern`, []string{"GET /hackathons", "GET /hackathons.html"}},
	},
	"host/access/connector/connector.go:Register": {
		{`"GET " + MuseClientPath`, []string{"GET /.well-known/muse-client.json"}},
		{`m + " /mcp"`, []string{"POST /mcp", "GET /mcp", "DELETE /mcp"}},
	},
	"host/sites/siteshttp/ui.go:RegisterUIRoutes": {
		{`"GET /" + name`, []string{"GET /llms.txt", "GET /install.html", "GET /auth.js", "GET /signin.js", "GET /features.json"}},
		{`"GET /" + name`, []string{"GET /openapi.yaml", "GET /openapi.json"}},
		{`"GET /" + name`, []string{"GET /favicon.svg", "GET /favicon.ico", "GET /favicon-32.png", "GET /apple-touch-icon.png"}},
		{`"GET /skills/" + dir + ".zip"`, skillPatterns(".zip", "host", "hack")},
		{`"GET /skills/" + dir + "/SKILL.md"`, skillPatterns("/SKILL.md", "host", "hack")},
		{`"GET /skills/" + dir`, skillPatterns("", "host", "hack")},
		{`"GET /skills/" + dir + "/references/{file}"`, skillPatterns("/references/{file}", "host", "hack")},
	},
	"products/hack/pages/hack_ui.go:RegisterHackUI": {
		{`p`, []string{"GET /signin", "GET /account", "GET /events", "GET /events/new", "GET /e/{slug}/team", "GET /e/{slug}/manage", "GET /e/{slug}/vote", "GET /e/{slug}/apply", "GET /join/{code}", "GET /judge/{code}", "GET /organiser/{code}", "GET /team/{code}"}},
	},
	"products/hack/asset_files.go:RegisterOwnFiles": {
		{`"GET /" + name`, []string{"GET /apple-touch-icon-hack.png", "GET /favicon-32-hack.png", "GET /favicon-hack.ico", "GET /favicon-hack.svg", "GET /hack", "GET /hack-connect-steps.json", "GET /hack-event.css", "GET /hack-get-started-preview.png", "GET /hack-pages.css", "GET /hack-watch-judge.mp3", "GET /hack-watch-organizer.mp3", "GET /hack-watch-participant.mp3", "GET /hack/kit.css", "GET /hack/public.css", "GET /hack/themes.css", "GET /og-hack.png", "GET /setup/simple-hack.json"}},
		{`"GET /" + name + "/{$}"`, []string{"GET /hack/{$}"}},
	},
	"products/hack/pages/hack_film.go:RegisterHackFilm": {
		{`"GET /film/" + name`, []string{"GET /film/hack-film-390.mp4", "GET /film/hack-film-1280.mp4", "GET /film/hack-film-390.webm", "GET /film/hack-film-1280.webm", "GET /film/hack-film-390-poster.jpg", "GET /film/hack-film-1280-poster.jpg"}},
		{`"GET /film/" + name`, []string{"GET /film/hack-companies-film-390.mp4", "GET /film/hack-companies-film-1280.mp4", "GET /film/hack-companies-film-390.webm", "GET /film/hack-companies-film-1280.webm", "GET /film/hack-companies-film-390-poster.jpg", "GET /film/hack-companies-film-1280-poster.jpg"}},
	},
	"products/hack/pages/hack_get_started.go:RegisterHackGetStarted": {
		{`"GET /simple-hack-skills-only-" + version + ".zip"`, versionPatterns([]string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"})},
		{`"GET /simple-hack-skills-only-" + version + ".zip"`, versionPatterns([]string{"0.2.13"})},
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
