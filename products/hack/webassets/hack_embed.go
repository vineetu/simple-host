package webassets

import (
	"embed"
	"io/fs"
)

//go:embed hack-toolkit/site/downloads/simple-hack-skills-only-0.2.13.zip
var hackFiles embed.FS

//go:embed all:hack-toolkit/skills
var hackSkills embed.FS

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

// HackFiles holds the current skills-only archive
// (site/downloads/simple-hack-skills-only-0.2.13.zip) that its versioned
// download address answers.
var HackFiles = sub(hackFiles, "hack-toolkit")

// HackSkills is the toolkit's reviewed skill tree (skills/<name>/...).
var HackSkills = sub(hackSkills, "hack-toolkit")
