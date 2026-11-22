package core

import (
	"net/url"
	"regexp"
	"strings"
)

// videoService is one embeddable video host: the link paths whose next
// segment is the video id, the id's shape, and the fixed player URL the id
// is placed into ({id}).
type videoService struct {
	paths  []string // path prefixes (after trimming "/") that precede the id
	exact  bool     // the id must be the last path segment
	watch  bool     // "/watch?v=<id>" also carries the id
	id     *regexp.Regexp
	player string
}

var (
	videoYouTube = videoService{paths: []string{"shorts/", "embed/", "live/"}, exact: true, watch: true,
		id: regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`), player: "https://www.youtube-nocookie.com/embed/{id}"}
	videoYouTuBe = videoService{paths: []string{""}, exact: true, id: videoYouTube.id, player: videoYouTube.player}
	videoVimeo   = videoService{paths: []string{""}, exact: true,
		id: regexp.MustCompile(`^[0-9]{1,15}$`), player: "https://player.vimeo.com/video/{id}"}
	videoLoom = videoService{paths: []string{"share/"}, exact: true,
		id: regexp.MustCompile(`^[0-9a-f]{32}$`), player: "https://www.loom.com/embed/{id}"}
	videoDrive = videoService{paths: []string{"file/d/"},
		id: regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`), player: "https://drive.google.com/file/d/{id}/preview"}
)

var videoServices = map[string]videoService{
	"youtube.com": videoYouTube, "www.youtube.com": videoYouTube, "m.youtube.com": videoYouTube,
	"youtu.be":  videoYouTuBe,
	"vimeo.com": videoVimeo, "www.vimeo.com": videoVimeo,
	"loom.com": videoLoom, "www.loom.com": videoLoom,
	"drive.google.com": videoDrive,
}

// VideoFrameOrigins are the only third-party origins the app may frame, one
// per VideoEmbed player.
var VideoFrameOrigins = []string{
	"https://www.youtube-nocookie.com",
	"https://player.vimeo.com",
	"https://www.loom.com",
	"https://drive.google.com",
}

// VideoEmbed turns an entry's demo video link into a player URL the judge screen may frame.
func VideoEmbed(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return ""
	}
	s, ok := videoServices[strings.ToLower(u.Hostname())]
	if !ok {
		return ""
	}
	id := s.linkID(u)
	if !s.id.MatchString(id) {
		return ""
	}
	return strings.Replace(s.player, "{id}", id, 1)
}

// linkID is the candidate video id in u's path (or watch query), "" when the
// link has none of the service's shapes.
func (s videoService) linkID(u *url.URL) string {
	if s.watch && u.EscapedPath() == "/watch" {
		return u.Query().Get("v")
	}
	path := strings.Trim(u.EscapedPath(), "/")
	for _, prefix := range s.paths {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		id, _, more := strings.Cut(rest, "/")
		if more && s.exact {
			return ""
		}
		return id
	}
	return ""
}
