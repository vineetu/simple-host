package main

import (
	"os"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/products/hack"
)

func main() {
	var exts []*extension.Extension
	if config.EventsHosted() {
		exts = []*extension.Extension{hack.Extension()}
	}
	os.Exit(server.Run(os.Args[1:], exts))
}
