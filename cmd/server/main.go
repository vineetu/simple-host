package main

import (
	"os"

	"github.com/vsriram/simple-host/internal/brain"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/products/hack"
)

func main() {
	hosted := config.EventsHosted()
	os.Exit(server.Run(os.Args[1:], programExtensions(hosted), programOptions(hosted)))
}

func programExtensions(hosted bool) []*extension.Extension {
	if hosted {
		return []*extension.Extension{hack.Extension()}
	}
	return nil
}

func programOptions(hosted bool) server.Options {
	return server.Options{KnowledgeProducts: knowledgeProducts, Redirects: compatibilityRedirects(hosted)}
}

func compatibilityRedirects(hosted bool) map[string]string {
	if hosted {
		return nil
	}
	return map[string]string{"GET /hackathons": "/", "GET /hackathons.html": "/"}
}

// The owner knowledge server describes these products, independently of which extension serves this install.
var knowledgeProducts = []brain.ProductFilter{
	{ID: "host"}, {ID: "hack", Includes: []string{"selfhost-hack", "hack-companies"}},
	{ID: "enterprise"}, {ID: "selfhost-host"}, {ID: "selfhost-hack", Includes: []string{"hack-companies"}},
	{ID: "hack-companies"}, {ID: "console"}, {ID: "travel"}, {ID: "all"},
}
