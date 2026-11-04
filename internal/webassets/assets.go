// Package webassets holds the embedded pages and skill bundles served by both editions.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var Static embed.FS

// Files is an embedded subtree with file-reading support.
type Files interface {
	fs.FS
	ReadFile(string) ([]byte, error)
}
type subtree struct{ fs.FS }

func (s subtree) ReadFile(name string) ([]byte, error) { return fs.ReadFile(s.FS, name) }
func sub(tree fs.FS, dir string) Files {
	tree, err := fs.Sub(tree, dir)
	if err != nil {
		panic(err)
	}
	return subtree{tree}
}
