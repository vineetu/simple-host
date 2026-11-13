package main

import (
	"os"

	"github.com/vsriram/simple-host/internal/server"
)

func main() { os.Exit(server.Run(os.Args[1:])) }
