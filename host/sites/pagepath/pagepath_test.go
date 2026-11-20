package pagepath

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPageAddresses(t *testing.T) {
	serve := func(name string) Result { return Result{Serve, name} }
	add := func(name string) Result { return Result{AddSlash, name} }
	drop := func(name string) Result { return Result{DropSlash, name} }
	miss := Result{}
	file := fstest.MapFS{"x.html": {}}
	folder := fstest.MapFS{"x/index.html": {},
		"dir/index.html": {}}
	both := fstest.MapFS{"x.html": {},
		"x/index.html":   {},
		"dir/index.html": {}}
	for _, tc := range []struct {
		path               string
		file, folder, both Result
	}{
		{"/x", serve("x.html"), add("x/index.html"), serve("x.html")},
		{"/x/", drop("x.html"), serve("x/index.html"), serve("x/index.html")},
		{"/x.html", serve("x.html"), miss, serve("x.html")},
		{"/dir", miss, add("dir/index.html"), add("dir/index.html")},
		{"/dir/", miss, serve("dir/index.html"), serve("dir/index.html")},
	} {
		for _, tree := range []struct {
			name string
			fs   fstest.MapFS
			want Result
		}{
			{"file", file, tc.file},
			{"folder", folder, tc.folder},
			{"both", both, tc.both},
		} {
			if got := Resolve(tree.fs, tc.path); got != tree.want {
				t.Errorf("%s %s: got %+v, want %+v", tree.name, tc.path, got, tree.want)
			}
		}
	}
	tree := fstest.MapFS{
		"index.html":            {},
		"x.html":                {},
		"blog/post.html":        {},
		"blog.html":             {},
		"style.css":             {},
		"_redirects":            {},
		"pipe":                  {Mode: fs.ModeNamedPipe},
		"no-page/index.html":    {Mode: fs.ModeDir},
		"empty/asset":           {},
		"a b.html":              {},
		"%20.html":              {},
		"guide.html/index.html": {},
	}
	for _, tc := range []struct {
		path  string
		want  Result
		taken bool // a redirect rule that is not forced stands aside here
	}{
		{"/", serve("index.html"), true},
		{"/index.html", serve("index.html"), true},
		{"/x.html/", drop("x.html"), true},
		{"/blog", serve("blog.html"), true},
		{"/blog/", drop("blog.html"), true},
		{"/blog/.", serve("blog.html"), true},
		{"/blog/post/../", drop("blog.html"), true},
		{"/empty", miss, true},
		{"/empty/", miss, true},
		{"/pipe", miss, false},
		{"/no-page", miss, true},
		{"/no-page/", miss, true},
		{"/style.css/", drop("style.css"), true},
		{"/_redirects", miss, false},
		{"/%5Fredirects/", miss, false},
		{"/a%20b", serve("a b.html"), true},
		{"/%2520", serve("%20.html"), true},
		{"/guide.html", add("guide.html/index.html"), true},
		{"/guide.html/.", add("guide.html/index.html"), true},
		{"/guide.html/", serve("guide.html/index.html"), true},
		{"/missing", miss, false},
		{"/missing/", miss, false},
		{"/../index.html", serve("index.html"), true},
	} {
		decoded, err := url.PathUnescape(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := Resolve(tree, decoded); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.path, got, tc.want)
		}
		if got := Taken(tree, decoded); got != tc.taken {
			t.Errorf("%s: taken %v, want %v", tc.path, got, tc.taken)
		}
	}
	// A redirect always names an address that serves the same file.
	for _, p := range []string{"/x/", "/dir", "/blog/", "/style.css/", "/guide.html"} {
		for _, fsys := range []fstest.MapFS{file, folder, both, tree} {
			from := Resolve(fsys, p)
			to := Result{}
			switch from.Kind {
			case AddSlash:
				to = Resolve(fsys, p+"/")
			case DropSlash:
				to = Resolve(fsys, strings.TrimRight(p, "/"))
			default:
				continue
			}
			if to != serve(from.File) {
				t.Errorf("%s redirects to an address answering %+v", p, to)
			}
		}
	}
}

func TestVersionRootConfinesPageLookup(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, p := range []string{"/escape", "/escape/", "/escape.html"} {
		if got := Resolve(root, p); got.Kind != NotFound {
			t.Errorf("%s: %+v", p, got)
		}
	}
}
