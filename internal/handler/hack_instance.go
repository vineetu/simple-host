package handler

import (
	"net/url"
	"strings"
)

// SetHackInstanceURL is called once at startup, before routes or ZIP caches.
func SetHackInstanceURL(base string) {
	hackInkBaseURL = strings.TrimRight(base, "/")
	chromeCache.Clear()
}

// Full Hack instances serve the same UI and skills, with their own connector
// and event addresses. Simple Host's hosted-product and control-plane links
// keep their existing meaning.
func hackInstanceText(data []byte) []byte {
	if !hackMode || hackInkBaseURL == "https://simple-hack.app" {
		return data
	}
	u, err := url.Parse(hackInkBaseURL)
	if err != nil || u.Host == "" {
		return data
	}
	return []byte(strings.NewReplacer("https://simple-hack.app", strings.TrimRight(hackInkBaseURL, "/"), "simple-hack.app", u.Host).Replace(string(data)))
}
