package webassets

import (
	"embed"
	"io/fs"
)

//go:embed hack-toolkit/site/downloads/simple-hack-skills-only-0.2.10.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.11.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.12.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.13.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.6.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.7.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.8.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.9.zip
var hackFiles embed.FS

//go:embed all:hack-toolkit/skills
var hackSkills embed.FS

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

// HackFiles holds the eight historical skills-only archives
// (site/downloads/simple-hack-skills-only-0.2.6.zip to 0.2.13.zip) that the
// versioned download addresses keep answering.
var HackFiles = sub(hackFiles, "hack-toolkit")

// HackSkills is the toolkit's reviewed skill tree (skills/<name>/...).
var HackSkills = sub(hackSkills, "hack-toolkit")
