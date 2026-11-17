package webassets

import "embed"

//go:embed all:simple-host-website/skills/connect-domain all:simple-host-website/skills/website-deploy all:simple-host-website/skills/website-deploy-builder all:simple-host-website/.claude-plugin
var hostFiles embed.FS

var Host = sub(hostFiles, "simple-host-website")
