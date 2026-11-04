package webassets

import "embed"

//go:embed hack-toolkit/site/downloads/simple-hack-skills-only-0.2.10.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.11.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.12.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.13.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.6.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.7.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.8.zip hack-toolkit/site/downloads/simple-hack-skills-only-0.2.9.zip
var hackFiles embed.FS

//go:embed all:hack-toolkit/skills
var hackSkills embed.FS

var HackFiles = sub(hackFiles, "hack-toolkit")
var HackSkills = sub(hackSkills, "hack-toolkit")
