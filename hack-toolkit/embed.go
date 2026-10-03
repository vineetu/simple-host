// Package hacktoolkit embeds the prepared skills-only ChatGPT upload ZIP for
// first-party download from the Simple Hack Get started page.
package hacktoolkit

import "embed"

//go:embed site/downloads/simple-hack-skills-only-0.2.5.zip
var Files embed.FS

// Skills is the reviewed connector-only Simple Hack skill snapshot.
//go:embed all:skills
var Skills embed.FS
