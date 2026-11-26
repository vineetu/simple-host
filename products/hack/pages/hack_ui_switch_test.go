package pages

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/web/dist"
)

// The server and the new app (web/hack/src/routes/resolve.ts) agree on which
// screen every address is: both are tested against the same table.
func TestScreenForMatchesTheWebApp(t *testing.T) {
	raw, err := os.ReadFile("../../../web/hack/src/routes/resolve-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ URL, ID string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		u, _ := url.Parse(c.URL)
		if got := ScreenFor(u); got != c.ID {
			t.Errorf("%s: got %s, want %s", c.URL, got, c.ID)
		}
	}
}

// The live list is the built app's, and the page served for a live screen is
// the new index.html with the list in it; any other screen gets app.html.
func TestAppPageSwitchesOnTheLiveList(t *testing.T) {
	set, meta := live()
	if !strings.HasPrefix(string(meta), `<meta name="hack-live" content="`) {
		t.Fatalf("meta: %s", meta)
	}
	static := fstestStatic{}
	for _, c := range []struct {
		path string
		new  bool
	}{
		{"/events", set["landing-your-events"]},
		{"/signin", set["signin"]},
		{"/e/x/manage?s=settings", set["organiser-settings"]},
	} {
		body, isNew, err := appPage(httptest.NewRequest("GET", c.path, nil), static)
		if err != nil {
			t.Fatal(c.path, err)
		}
		if isNew != c.new {
			t.Errorf("%s: new=%v, want %v", c.path, isNew, c.new)
		}
		if !strings.Contains(string(body), string(meta)) {
			t.Errorf("%s: no live list in the page", c.path)
		}
	}
	if _, err := fs.Stat(dist.Files, "files/index.html"); err != nil {
		t.Fatal(err)
	}
}

type fstestStatic struct{}

func (fstestStatic) Open(name string) (fs.File, error) {
	if name == "static/hack/app.html" {
		return os.Open("../webassets/static/hack/app.html")
	}
	return nil, fs.ErrNotExist
}
