package main

import (
	"os"

	"github.com/vsriram/simple-host/internal/brain"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/products/hack"
)

func main() { os.Exit(server.Run(os.Args[1:], programExtensions(), programOptions())) }

func programExtensions() []*extension.Extension {
	return []*extension.Extension{hack.Extension()}
}

func programOptions() server.Options {
	return server.Options{KnowledgeProducts: knowledgeProducts}
}

// The owner knowledge server describes these products, independently of which extension serves this install.
var knowledgeProducts = []brain.ProductFilter{
	{ID: "host"}, {ID: "hack", Includes: []string{"selfhost-hack", "hack-companies"}},
	{ID: "enterprise"}, {ID: "selfhost-host"}, {ID: "selfhost-hack", Includes: []string{"hack-companies"}},
	{ID: "hack-companies"}, {ID: "console"}, {ID: "travel"}, {ID: "all"},
}
