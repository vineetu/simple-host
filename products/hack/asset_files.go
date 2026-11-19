package hack

import "net/http"

// assetReplacements is the explicit list of platform names supplied by Hack:
// for each, Hack's file is served and the platform's is not consulted.
// ".well-known" and "setup" are the platform's folders, so a directory read lists Hack's.
func assetReplacements() map[string]string {
	return map[string]string{
		"setup/small-box-settings.json": "static/setup/small-box-settings.json",
		".well-known":                   "static/.well-known",
		".well-known/api-catalog":       "static/.well-known/hack-api-catalog",
		".well-known/ard.json":          "static/.well-known/hack-ard.json",
		"apple-touch-icon.png":          "static/apple-touch-icon-hack.png",
		"auth.md":                       "static/hack-auth.md",
		"favicon-32.png":                "static/favicon-32-hack.png",
		"favicon.ico":                   "static/favicon-hack.ico",
		"favicon.svg":                   "static/favicon-hack.svg",
		"llms.txt":                      "static/hack-llms.txt",
		"setup":                         "static/setup",
		"setup/setup.js":                "static/setup/setup-hack.js",
		"openapi.json":                  "static/openapi.json",
		"features.json":                 "static/features.json",
		"features.html":                 "static/features.html",
		"setup-helper.html":             "static/setup-helper.html",
	}
}

// ownAssetFiles are Hack's own file names. The platform's page pipeline reads
// them (the pages it renders for Hack), and its file server never answers
// them: over HTTP each is reachable only through RegisterOwnFiles or another
// route Hack registered. A file added to the embed is unreachable until it is
// listed in one of the two tables below.
func ownAssetFiles() map[string]string {
	own := servedOwnFiles()
	for name, file := range pageOnlyFiles() {
		own[name] = file
	}
	return own
}

// servedOwnFiles are the own names served by their file name: the file route
// answers each at its own address.
func servedOwnFiles() map[string]string {
	return map[string]string{
		"apple-touch-icon-hack.png":                     "static/apple-touch-icon-hack.png",
		"favicon-32-hack.png":                           "static/favicon-32-hack.png",
		"favicon-hack.ico":                              "static/favicon-hack.ico",
		"favicon-hack.svg":                              "static/favicon-hack.svg",
		"hack":                                          "static/hack",
		"hack-connect-steps.json":                       "static/hack-connect-steps.json",
		"hack-film-narration.mp3":                       "static/hack-film-narration.mp3",
		"hack-get-started-preview.png":                  "static/hack-get-started-preview.png",
		"hack-ink.css":                                  "static/hack-ink.css",
		"hack-watch-judge.mp3":                          "static/hack-watch-judge.mp3",
		"hack-watch-organizer.mp3":                      "static/hack-watch-organizer.mp3",
		"hack-watch-participant.mp3":                    "static/hack-watch-participant.mp3",
		"hack/api.js":                                   "static/hack/api.js",
		"hack/app.html":                                 "static/hack/app.html",
		"hack/app.js":                                   "static/hack/app.js",
		"hack/assistant.js":                             "static/hack/assistant.js",
		"hack/checklist.js":                             "static/hack/checklist.js",
		"hack/connect-steps.js":                         "static/hack/connect-steps.js",
		"hack/event-list.js":                            "static/hack/event-list.js",
		"hack/kit.css":                                  "static/hack/kit.css",
		"hack/leave.js":                                 "static/hack/leave.js",
		"hack/nav.js":                                   "static/hack/nav.js",
		"hack/navigation-data.js":                       "static/hack/navigation-data.js",
		"hack/navigation.js":                            "static/hack/navigation.js",
		"hack/needs.js":                                 "static/hack/needs.js",
		"hack/platform.js":                              "static/hack/platform.js",
		"hack/public.css":                               "static/hack/public.css",
		"hack/schedule.js":                              "static/hack/schedule.js",
		"hack/screens":                                  "static/hack/screens",
		"hack/screens/_event-tracks.js":                 "static/hack/screens/_event-tracks.js",
		"hack/screens/_judge-util.js":                   "static/hack/screens/_judge-util.js",
		"hack/screens/_orgA-util.js":                    "static/hack/screens/_orgA-util.js",
		"hack/screens/_orgB-util.js":                    "static/hack/screens/_orgB-util.js",
		"hack/screens/_partB-util.js":                   "static/hack/screens/_partB-util.js",
		"hack/screens/_publish-results.js":              "static/hack/screens/_publish-results.js",
		"hack/screens/_settings-admin.js":               "static/hack/screens/_settings-admin.js",
		"hack/screens/_settings-entries.js":             "static/hack/screens/_settings-entries.js",
		"hack/screens/_settings-judging.js":             "static/hack/screens/_settings-judging.js",
		"hack/screens/_settings-links.js":               "static/hack/screens/_settings-links.js",
		"hack/screens/_settings-people.js":              "static/hack/screens/_settings-people.js",
		"hack/screens/_settings-results.js":             "static/hack/screens/_settings-results.js",
		"hack/screens/_settings-teams.js":               "static/hack/screens/_settings-teams.js",
		"hack/screens/_stage-announce-dialog.js":        "static/hack/screens/_stage-announce-dialog.js",
		"hack/screens/_team-message-dialog.js":          "static/hack/screens/_team-message-dialog.js",
		"hack/screens/_team-util.js":                    "static/hack/screens/_team-util.js",
		"hack/screens/_thread-with-organisers.js":       "static/hack/screens/_thread-with-organisers.js",
		"hack/screens/account.js":                       "static/hack/screens/account.js",
		"hack/screens/judge-home.js":                    "static/hack/screens/judge-home.js",
		"hack/screens/judge-queue.js":                   "static/hack/screens/judge-queue.js",
		"hack/screens/judge-rubric.js":                  "static/hack/screens/judge-rubric.js",
		"hack/screens/judge-scoring.js":                 "static/hack/screens/judge-scoring.js",
		"hack/screens/landing-your-events.js":           "static/hack/screens/landing-your-events.js",
		"hack/screens/organiser-announcements.js":       "static/hack/screens/organiser-announcements.js",
		"hack/screens/organiser-create-event.js":        "static/hack/screens/organiser-create-event.js",
		"hack/screens/organiser-entries.js":             "static/hack/screens/organiser-entries.js",
		"hack/screens/organiser-entry-detail.js":        "static/hack/screens/organiser-entry-detail.js",
		"hack/screens/organiser-event-page.js":          "static/hack/screens/organiser-event-page.js",
		"hack/screens/organiser-judging-assignments.js": "static/hack/screens/organiser-judging-assignments.js",
		"hack/screens/organiser-judging.js":             "static/hack/screens/organiser-judging.js",
		"hack/screens/organiser-overview.js":            "static/hack/screens/organiser-overview.js",
		"hack/screens/organiser-people.js":              "static/hack/screens/organiser-people.js",
		"hack/screens/organiser-results.js":             "static/hack/screens/organiser-results.js",
		"hack/screens/organiser-settings.js":            "static/hack/screens/organiser-settings.js",
		"hack/screens/organiser-team-detail.js":         "static/hack/screens/organiser-team-detail.js",
		"hack/screens/organiser-teams.js":               "static/hack/screens/organiser-teams.js",
		"hack/screens/participant-announcements.js":     "static/hack/screens/participant-announcements.js",
		"hack/screens/participant-entry.js":             "static/hack/screens/participant-entry.js",
		"hack/screens/participant-home.js":              "static/hack/screens/participant-home.js",
		"hack/screens/participant-results.js":           "static/hack/screens/participant-results.js",
		"hack/screens/participant-schedule.js":          "static/hack/screens/participant-schedule.js",
		"hack/screens/participant-submissions.js":       "static/hack/screens/participant-submissions.js",
		"hack/screens/participant-team.js":              "static/hack/screens/participant-team.js",
		"hack/screens/participant-website.js":           "static/hack/screens/participant-website.js",
		"hack/screens/public-apply.js":                  "static/hack/screens/public-apply.js",
		"hack/screens/public-join.js":                   "static/hack/screens/public-join.js",
		"hack/screens/public-judge.js":                  "static/hack/screens/public-judge.js",
		"hack/screens/public-organiser-invite.js":       "static/hack/screens/public-organiser-invite.js",
		"hack/screens/public-team-link.js":              "static/hack/screens/public-team-link.js",
		"hack/shell.js":                                 "static/hack/shell.js",
		"hack/stage-announce.js":                        "static/hack/stage-announce.js",
		"hack/themes.css":                               "static/hack/themes.css",
		"hack/tour.js":                                  "static/hack/tour.js",
		"hack/tours.js":                                 "static/hack/tours.js",
		"hack/ui.js":                                    "static/hack/ui.js",
		"og-hack.png":                                   "static/og-hack.png",
		"setup/simple-hack.json":                        "static/setup/simple-hack.json",
	}
}

// ownFolders are the folders among servedOwnFiles. A folder also answers at
// its name with a trailing slash.
var ownFolders = []string{"hack", "hack/screens"}

// pageOnlyFiles are own names that have no address of their own: each is a
// template or a page that one of Hack's routes renders (/, /watch,
// /companies, /get-started, /hackathons, /directory, /voting, the event
// page, /llms.txt, /auth.md and the two discovery documents).
func pageOnlyFiles() map[string]string {
	return map[string]string{
		".well-known/hack-api-catalog": "static/.well-known/hack-api-catalog",
		".well-known/hack-ard.json":    "static/.well-known/hack-ard.json",
		"hack-auth.md":                 "static/hack-auth.md",
		"hack-companies.html":          "static/hack-companies.html",
		"hack-directory.html":          "static/hack-directory.html",
		"hack-event.html":              "static/hack-event.html",
		"hack-get-started.html":        "static/hack-get-started.html",
		"hack-home.html":               "static/hack-home.html",
		"hack-llms.txt":                "static/hack-llms.txt",
		"hack-story.html":              "static/hack-story.html",
		"hack-voting.html":             "static/hack-voting.html",
		"hack-watch.html":              "static/hack-watch.html",
		"hackathons.html":              "static/hackathons.html",
	}
}

// RegisterOwnFiles is Hack's one file route: it mounts files (the platform's
// handler.AssetServices.ProductFiles) at the address of every own name that
// is served by its file name, and at a folder's two addresses.
func RegisterOwnFiles(mux *http.ServeMux, files http.Handler) {
	for name := range servedOwnFiles() {
		mux.Handle("GET /"+name, files)
	}
	for _, name := range ownFolders {
		mux.Handle("GET /"+name+"/{$}", files)
	}
}
