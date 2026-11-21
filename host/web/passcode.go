package web

import (
	"strings"
)

// StripPasscodeRewrite turns nginx's or Caddy's /internal/passcode/<path>
// back into the <path> the visitor asked for; any other path is unchanged.
func StripPasscodeRewrite(p string) string {
	if rest, ok := strings.CutPrefix(p, passcodeRewritePrefix); ok && (rest == "" || rest[0] == '/' || rest[0] == '?') {
		return "/" + strings.TrimPrefix(rest, "/")
	}
	return p
}
