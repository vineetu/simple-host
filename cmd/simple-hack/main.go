package main

import (
	"os"

	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	"github.com/vsriram/simple-host/products/hack"
)

func main() { os.Exit(server.Run(os.Args[1:], programExtensions(), programOptions())) }

func programExtensions() []*extension.Extension {
	return []*extension.Extension{hack.Extension()}
}

func programOptions() server.Options {
	return server.Options{}
}
