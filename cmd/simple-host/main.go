package main

import (
	"log"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/server"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("EVENTS")), "hosted") {
		log.Print("this program does not include an events product; unset EVENTS")
		return 1
	}
	return server.Run(args, nil, compatibilityRedirects())
}

func compatibilityRedirects() map[string]string {
	return map[string]string{"GET /hackathons": "/", "GET /hackathons.html": "/"}
}
