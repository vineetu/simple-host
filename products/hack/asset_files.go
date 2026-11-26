package hack

import "net/http"

// assetReplacements is the explicit list of platform names supplied by Hack:
// for each, Hack's file is served and the platform's is not consulted.
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
		"notfound.html":                 "static/hack-notfound.html",
	}
}

// ownAssetFiles are Hack's own file names.
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
		"apple-touch-icon-hack.png":    "static/apple-touch-icon-hack.png",
		"favicon-32-hack.png":          "static/favicon-32-hack.png",
		"favicon-hack.ico":             "static/favicon-hack.ico",
		"favicon-hack.svg":             "static/favicon-hack.svg",
		"hack":                         "static/hack",
		"hack-connect-steps.json":      "static/hack-connect-steps.json",
		"hack-get-started-preview.png": "static/hack-get-started-preview.png",
		"hack-event.css":               "static/hack-event.css",
		"hack-pages.css":               "static/hack-pages.css",
		"hack-watch-judge.mp3":         "static/hack-watch-judge.mp3",
		"hack-watch-organizer.mp3":     "static/hack-watch-organizer.mp3",
		"hack-watch-participant.mp3":   "static/hack-watch-participant.mp3",
		"hack/kit.css":                 "static/hack/kit.css",
		"hack/public.css":              "static/hack/public.css",
		"hack/themes.css":              "static/hack/themes.css",
		"og-hack.png":                  "static/og-hack.png",
		"setup/simple-hack.json":       "static/setup/simple-hack.json",
	}
}

// ownFolders are the folders among servedOwnFiles.
var ownFolders = []string{"hack"}

// pageOnlyFiles are own names that have no address of their own: each is a
// template or a page that one of Hack's routes renders (/, /watch,
// /companies, /get-started, /hackathons, /directory, the event
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
