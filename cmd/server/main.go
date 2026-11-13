package main

import (
	"os"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
)

func main() {
	var exts []*extension.Extension
	if config.EventsHosted() {
		exts = []*extension.Extension{hackExtension()}
	}
	os.Exit(server.Run(os.Args[1:], exts))
}
