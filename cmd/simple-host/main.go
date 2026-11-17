package main

import (
	"log"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/brain"
	"github.com/vsriram/simple-host/host/server"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("EVENTS")), "hosted") {
		log.Print("this program does not include an events product; unset EVENTS")
		return 1
	}
	return server.Run(args, nil, programOptions())
}

func programOptions() server.Options {
	return server.Options{KnowledgeProducts: knowledgeProducts, Redirects: compatibilityRedirects()}
}

func compatibilityRedirects() map[string]string {
	return map[string]string{"GET /hackathons": "/", "GET /hackathons.html": "/"}
}

// The owner knowledge server describes these products, independently of which extension serves this install.
var knowledgeProducts = []brain.ProductFilter{
	{ID: "host"}, {ID: "hack", Includes: []string{"selfhost-hack", "hack-companies"}},
	{ID: "enterprise"}, {ID: "selfhost-host"}, {ID: "selfhost-hack", Includes: []string{"hack-companies"}},
	{ID: "hack-companies"}, {ID: "console"}, {ID: "travel"}, {ID: "all"},
}
