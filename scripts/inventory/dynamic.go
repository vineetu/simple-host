package main

// The union of registrations across all editions/configurations. These patterns
// are read from connector.go, ui.go, hack_ui.go, hack_get_started.go,
// hack_film.go and the product's asset_files.go. Keep
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
	"products/hack/asset_files.go:RegisterOwnFiles": {
		{`"GET /" + name`, []string{"GET /apple-touch-icon-hack.png", "GET /favicon-32-hack.png", "GET /favicon-hack.ico", "GET /favicon-hack.svg", "GET /hack", "GET /hack-connect-steps.json", "GET /hack-film-narration.mp3", "GET /hack-get-started-preview.png", "GET /hack-ink.css", "GET /hack-watch-judge.mp3", "GET /hack-watch-organizer.mp3", "GET /hack-watch-participant.mp3", "GET /hack/api.js", "GET /hack/app.html", "GET /hack/app.js", "GET /hack/assistant.js", "GET /hack/checklist.js", "GET /hack/connect-steps.js", "GET /hack/event-list.js", "GET /hack/kit.css", "GET /hack/leave.js", "GET /hack/nav.js", "GET /hack/navigation-data.js", "GET /hack/navigation.js", "GET /hack/needs.js", "GET /hack/platform.js", "GET /hack/public.css", "GET /hack/schedule.js", "GET /hack/screens", "GET /hack/screens/_event-tracks.js", "GET /hack/screens/_judge-util.js", "GET /hack/screens/_orgA-util.js", "GET /hack/screens/_orgB-util.js", "GET /hack/screens/_partB-util.js", "GET /hack/screens/_publish-results.js", "GET /hack/screens/_settings-admin.js", "GET /hack/screens/_settings-entries.js", "GET /hack/screens/_settings-judging.js", "GET /hack/screens/_settings-links.js", "GET /hack/screens/_settings-people.js", "GET /hack/screens/_settings-results.js", "GET /hack/screens/_settings-teams.js", "GET /hack/screens/_stage-announce-dialog.js", "GET /hack/screens/_team-message-dialog.js", "GET /hack/screens/_team-util.js", "GET /hack/screens/_thread-with-organisers.js", "GET /hack/screens/account.js", "GET /hack/screens/judge-home.js", "GET /hack/screens/judge-queue.js", "GET /hack/screens/judge-rubric.js", "GET /hack/screens/judge-scoring.js", "GET /hack/screens/landing-your-events.js", "GET /hack/screens/organiser-announcements.js", "GET /hack/screens/organiser-create-event.js", "GET /hack/screens/organiser-entries.js", "GET /hack/screens/organiser-entry-detail.js", "GET /hack/screens/organiser-event-page.js", "GET /hack/screens/organiser-judging-assignments.js", "GET /hack/screens/organiser-judging.js", "GET /hack/screens/organiser-overview.js", "GET /hack/screens/organiser-people.js", "GET /hack/screens/organiser-results.js", "GET /hack/screens/organiser-settings.js", "GET /hack/screens/organiser-team-detail.js", "GET /hack/screens/organiser-teams.js", "GET /hack/screens/participant-announcements.js", "GET /hack/screens/participant-entry.js", "GET /hack/screens/participant-home.js", "GET /hack/screens/participant-results.js", "GET /hack/screens/participant-schedule.js", "GET /hack/screens/participant-submissions.js", "GET /hack/screens/participant-team.js", "GET /hack/screens/participant-website.js", "GET /hack/screens/public-apply.js", "GET /hack/screens/public-join.js", "GET /hack/screens/public-judge.js", "GET /hack/screens/public-organiser-invite.js", "GET /hack/screens/public-team-link.js", "GET /hack/shell.js", "GET /hack/stage-announce.js", "GET /hack/themes.css", "GET /hack/tour.js", "GET /hack/tours.js", "GET /hack/ui.js", "GET /og-hack.png", "GET /setup/simple-hack.json"}},
		{`"GET /" + name + "/{$}"`, []string{"GET /hack/{$}", "GET /hack/screens/{$}"}},
	},
	"products/hack/hackhttp/hack_film.go:RegisterHackFilm": {
		{`"GET /film/" + name`, []string{"GET /film/hack-film-390.mp4", "GET /film/hack-film-1280.mp4", "GET /film/hack-film-390.webm", "GET /film/hack-film-1280.webm", "GET /film/hack-film-390-poster.jpg", "GET /film/hack-film-1280-poster.jpg"}},
		{`"GET /film/" + name`, []string{"GET /film/hack-companies-film-390.mp4", "GET /film/hack-companies-film-1280.mp4", "GET /film/hack-companies-film-390.webm", "GET /film/hack-companies-film-1280.webm", "GET /film/hack-companies-film-390-poster.jpg", "GET /film/hack-companies-film-1280-poster.jpg"}},
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
