package sites

import (
	"io/fs"
	"path"
	"time"

	"github.com/vsriram/simple-host/host/sites/pagepath"
	"github.com/vsriram/simple-host/host/sites/redirects"
)

func (s *Service) validateRedirects(files map[string][]byte) error {
	if data, ok := files["_redirects"]; ok {
		max := s.deps.RedirectsMaxRules
		if max == 0 {
			max = 1000
		}
		tree := newPublishedTree(files)
		exists := func(p string) bool { return pagepath.Taken(tree, p) }
		if _, err := redirects.ParseWith(data, redirects.Options{MaxRules: max, Exists: exists}); err != nil {
			return &Error{Code: "redirects_invalid", Message: "_redirects: " + err.Error()}
		}
	}
	return nil
}

// publishedTree is the files being published, as the page-address rule reads
// a version tree: every file, and every folder that holds one.
type publishedTree struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newPublishedTree(files map[string][]byte) publishedTree {
	dirs := map[string]bool{".": true}
	for name := range files {
		for d := path.Dir(name); !dirs[d] && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	return publishedTree{files, dirs}
}

func (t publishedTree) Stat(name string) (fs.FileInfo, error) {
	if _, ok := t.files[name]; ok {
		return publishedInfo(0), nil
	}
	if t.dirs[name] {
		return publishedInfo(fs.ModeDir), nil
	}
	return nil, fs.ErrNotExist
}

type publishedInfo fs.FileMode

func (i publishedInfo) Name() string       { return "" }
func (i publishedInfo) Size() int64        { return 0 }
func (i publishedInfo) Mode() fs.FileMode  { return fs.FileMode(i) }
func (i publishedInfo) ModTime() time.Time { return time.Time{} }
func (i publishedInfo) IsDir() bool        { return fs.FileMode(i).IsDir() }
func (i publishedInfo) Sys() any           { return nil }
