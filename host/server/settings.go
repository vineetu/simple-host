package server

import (
	"fmt"
	"os"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/extension"
)

// runSettingsCommand is `simple-host settings --json`: every environment
// setting, with its area, description, type, default and range, as JSON
// (docs/advanced/settings.json is this output). Needs no config or database.
func runSettingsCommand(args []string, exts []*extension.Extension) int {
	if len(args) != 1 || args[0] != "--json" {
		fmt.Fprintln(os.Stderr, "usage: simple-host settings --json")
		return 2
	}
	var settings []config.Setting
	for _, ext := range exts {
		if ext != nil {
			settings = append(settings, ext.Config.Settings...)
		}
	}
	b, err := config.SettingsJSON(settings...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Stdout.Write(b)
	return 0
}
