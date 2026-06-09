package tarball

import (
	"fmt"
	"path/filepath"
	"strings"
)

// blockedExtensions are the only file types rejected at upload time.
// Everything else passes — including binary downloads (`.exe`, `.dmg`,
// `.jar`, `.deb`, `.apk`, etc.). Static serving means nothing executes
// server-side; downloaded binaries run on the visitor's machine, which
// is downstream of our scope.
//
// Source-script extensions stay blocked only as a guardrail against
// accidental "I uploaded my source tree instead of the build output"
// deploys. They're inert on the server, so it's a friendliness check,
// not a safety control.
var blockedExtensions = map[string]struct{}{
	".sh":   {},
	".bash": {},
	".zsh":  {},
	".fish": {},
	".bat":  {},
	".cmd":  {},
	".ps1":  {},
	".py":   {},
	".pyc":  {},
	".rb":   {},
	".pl":   {},
	".go":   {},
	".php":  {},
}

func ValidateExtensions(files map[string][]byte) error {
	for path := range files {
		extension := strings.ToLower(filepath.Ext(path))
		if _, ok := blockedExtensions[extension]; ok {
			return fmt.Errorf("disallowed file extension for %q", path)
		}
	}

	return nil
}
