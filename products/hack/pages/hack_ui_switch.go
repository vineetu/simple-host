package pages

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"github.com/vsriram/simple-host/products/hack/web/dist"
)

// Old and new side by side (HK1): Simple Hack's front-end is being rebuilt
// screen by screen in web/hack (embedded from products/hack/web/dist). For
// each app request the server works out which screen the address is (the
// same way both apps do: navigation.json's screens, ?s= for the section) and
// serves the new app's index.html when the new app lists that screen as live
// in its screens.json, the old static/hack/app.html otherwise. Both pages
// carry <meta name="hack-live"> with the list, so each app knows which
// screens to leave to the other (a full page load). Switching a screen over
// is a web/hack build; nothing here changes per screen.

var (
	liveOnce sync.Once
	liveSet  map[string]bool
	liveMeta []byte
)

func live() (map[string]bool, []byte) {
	liveOnce.Do(func() {
		liveSet = map[string]bool{}
		var m struct {
			Live []string `json:"live"`
		}
		if b, err := fs.ReadFile(dist.Files, "files/screens.json"); err == nil && json.Unmarshal(b, &m) == nil {
			for _, id := range m.Live {
				if screenID.MatchString(id) {
					liveSet[id] = true
				}
			}
		}
		ids := make([]string, 0, len(liveSet))
		for id := range liveSet {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		liveMeta = []byte(`<meta name="hack-live" content="` + strings.Join(ids, " ") + `">`)
	})
	return liveSet, liveMeta
}

var screenID = regexp.MustCompile(`^[a-z][a-z-]*$`)

var (
	invitePath = regexp.MustCompile(`^/(join|judge|organiser|team)/[^/]+$`)
	applyPath  = regexp.MustCompile(`^/e/[^/]+/apply$`)
	areaPath   = regexp.MustCompile(`^/e/[^/]+/(manage|team|judge)$`)
	votePath   = regexp.MustCompile(`^/e/[^/]+/vote$`)
)

var (
	sectionsOnce sync.Once
	areaSections map[string][]string
)

// sections are an area's ?s= values, its default first (nav.js resolveRoute).
func sections(role string) []string {
	sectionsOnce.Do(func() {
		areaSections = map[string][]string{}
		for _, r := range hackevents.Navigation().Roles {
			for _, n := range r.Nav {
				areaSections[r.ID] = append(areaSections[r.ID], n.ID)
			}
		}
		areaSections["organiser"] = append(areaSections["organiser"], "team", "entry", "assignments")
		areaSections["judge"] = append(areaSections["judge"], "scoring")
	})
	return areaSections[role]
}

// ScreenFor is the screen an app address shows (web/hack/src/routes/resolve.ts does the same).
func ScreenFor(u *url.URL) string {
	path := strings.TrimRight(u.Path, "/")
	switch {
	case path == "/signin":
		return "signin"
	case path == "/directory":
		return "public-directory"
	case votePath.MatchString(path):
		return "public-vote"
	case path == "/events/new":
		return "organiser-create-event"
	case path == "/account":
		return "account"
	case invitePath.MatchString(path):
		return map[string]string{"join": "public-join", "judge": "public-judge", "organiser": "public-organiser-invite", "team": "public-team-link"}[strings.Split(path, "/")[1]]
	case applyPath.MatchString(path):
		return "public-apply"
	}
	m := areaPath.FindStringSubmatch(path)
	if m == nil {
		return "landing-your-events"
	}
	role := map[string]string{"manage": "organiser", "team": "participant", "judge": "judge"}[m[1]]
	all := sections(role)
	section := all[0]
	if want := u.Query().Get("s"); want != "" {
		for _, s := range all {
			if s == want {
				section = want
			}
		}
	}
	id := role + "-" + section
	if alias, ok := map[string]string{"organiser-team": "organiser-team-detail", "organiser-entry": "organiser-entry-detail", "organiser-assignments": "organiser-judging-assignments"}[id]; ok {
		return alias
	}
	return id
}

// switchable serves the new app for screen id when it is live, else old (a
// page outside the old app, such as the people's choice vote page).
func switchable(id string, app, old http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if set, _ := live(); set[id] {
			app.ServeHTTP(w, r)
			return
		}
		old.ServeHTTP(w, r)
	})
}

// appPage is the page to serve for an app address: the new app's index.html
// when its screen is live, else the old app.html; either with the live list.
func appPage(r *http.Request, staticFS fs.FS) (body []byte, isNew bool, err error) {
	set, meta := live()
	if set[ScreenFor(r.URL)] {
		body, err = fs.ReadFile(dist.Files, "files/index.html")
		isNew = true
	} else {
		body, err = fs.ReadFile(staticFS, "static/hack/app.html")
	}
	if err != nil {
		return nil, false, err
	}
	return bytes.Replace(body, []byte("</head>"), append(append([]byte{}, meta...), []byte("</head>")...), 1), isNew, nil
}

// serveNewAssets answers /hack/app/* from the new app's build. Names carry a
// content hash, so they are cached for good.
func serveNewAssets(mux *http.ServeMux) {
	sub, err := fs.Sub(dist.Files, "files")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(sub)
	mux.Handle("GET /hack/app/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
}
