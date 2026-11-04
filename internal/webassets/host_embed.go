package webassets

import "embed"

//go:embed all:simple-host-website/skills all:simple-host-website/.claude-plugin
var hostFiles embed.FS

var Host = sub(hostFiles, "simple-host-website")
