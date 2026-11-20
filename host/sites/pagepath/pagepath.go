// Package pagepath decides what a page address means inside one published tree.
package pagepath

import (
	"io/fs"
	"path"
	"strings"
)

type Kind int

const (
	NotFound  Kind = iota
	Serve          // the address is the page: answer with File
	AddSlash       // a folder page asked for without its slash: redirect to the address plus "/"
	DropSlash      // a file page asked for with a slash: redirect to the address without it
)

// Tree is one published version of a site. Serving passes the confined
// *os.Root it then opens File on; publishing passes the files about to be
// written. Names are slash-separated and relative, "." for the top.
type Tree interface {
	Stat(name string) (fs.FileInfo, error)
}

// Result is what an address means. File is the page's file inside the tree:
// served at this address (Serve), or at the address the redirect names. A
// redirect therefore always lands on a page that serves.
type Result struct {
	Kind Kind
	File string
}

// Resolve applies the page-address rule to a decoded site path:
//
//	/x    serves x, else x.html; else redirects to /x/ when x/index.html exists
//	/x/   serves x/index.html; else redirects to /x when x or x.html exists
//
// The root serves index.html, and the root _redirects file is never a page.
func Resolve(tree Tree, reqPath string) Result {
	name := strings.TrimPrefix(path.Clean("/"+reqPath), "/")
	regular := func(p string) bool {
		st, err := tree.Stat(p)
		return err == nil && st.Mode().IsRegular()
	}
	index := path.Join(name, "index.html")
	if name == "" {
		if regular(index) {
			return Result{Serve, index}
		}
		return Result{}
	}
	if name == "_redirects" && !isDir(tree, name) {
		return Result{}
	}
	file := ""
	switch {
	case regular(name):
		file = name
	case !strings.HasSuffix(name, ".html") && regular(name+".html"):
		file = name + ".html"
	}
	if strings.HasSuffix(reqPath, "/") {
		switch {
		case regular(index):
			return Result{Serve, index}
		case file != "":
			return Result{DropSlash, file}
		}
		return Result{}
	}
	switch {
	case file != "":
		return Result{Serve, file}
	case regular(index):
		return Result{AddSlash, index}
	}
	return Result{}
}

// Taken reports whether the tree already has something at reqPath: a page at
// either spelling of the address, or a folder. A site's own redirect rules
// stand aside there unless forced.
func Taken(tree Tree, reqPath string) bool {
	return Resolve(tree, reqPath).Kind != NotFound || isDir(tree, path.Clean("./"+reqPath))
}

func isDir(tree Tree, name string) bool {
	st, err := tree.Stat(name)
	return err == nil && st.IsDir()
}
