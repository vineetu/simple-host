package hackhttp

import (
	"net/url"
	"regexp"
	"strings"
)

// videoEmbed turns an entry's demo video link into a player URL the judge
// screen may frame (build-plan.md section 4.5). Only the four hosts in the
// table below are ever embeddable, and the player URL is always rebuilt
// from the parsed id on a fixed origin, never from the participant's own
// URL: "" for anything else, which the screen shows as a plain link.
//
// The app CSP's frame-src (hackAppCSP) lists exactly these four origins.
func videoEmbed(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := u.EscapedPath()
	seg := strings.Split(strings.Trim(path, "/"), "/")
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		var id string
		switch {
		case path == "/watch":
			id = u.Query().Get("v")
		case len(seg) == 2 && (seg[0] == "shorts" || seg[0] == "embed" || seg[0] == "live"):
			id = seg[1]
		}
		if videoYouTubeID.MatchString(id) {
			return "https://www.youtube-nocookie.com/embed/" + id
		}
	case "youtu.be":
		if len(seg) == 1 && videoYouTubeID.MatchString(seg[0]) {
			return "https://www.youtube-nocookie.com/embed/" + seg[0]
		}
	case "vimeo.com", "www.vimeo.com":
		if len(seg) == 1 && videoVimeoID.MatchString(seg[0]) {
			return "https://player.vimeo.com/video/" + seg[0]
		}
	case "loom.com", "www.loom.com":
		if len(seg) == 2 && seg[0] == "share" && videoLoomID.MatchString(seg[1]) {
			return "https://www.loom.com/embed/" + seg[1]
		}
	case "drive.google.com":
		if len(seg) >= 3 && seg[0] == "file" && seg[1] == "d" && videoDriveID.MatchString(seg[2]) {
			return "https://drive.google.com/file/d/" + seg[2] + "/preview"
		}
	}
	return ""
}

var (
	videoYouTubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	videoVimeoID   = regexp.MustCompile(`^[0-9]{1,15}$`)
	videoLoomID    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	videoDriveID   = regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`)
)

// videoFrameOrigins are the only third-party origins the app may frame, one
// per videoEmbed host.
var videoFrameOrigins = []string{
	"https://www.youtube-nocookie.com",
	"https://player.vimeo.com",
	"https://www.loom.com",
	"https://drive.google.com",
}
