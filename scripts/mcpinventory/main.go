// mcpinventory prints the registered MCP tool names for documentation checks.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/vsriram/simple-host/internal/mcp"
)

func main() {
	edition := flag.String("edition", "host", "tool edition: host, hack, or screen")
	flag.Parse()
	if *edition != "host" && *edition != "hack" && *edition != "screen" {
		fmt.Fprintln(os.Stderr, "unknown edition:", *edition)
		os.Exit(2)
	}
	var names []string
	for _, tool := range mcp.Inventory() {
		if tool.Edition == *edition {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Println(name)
	}
}
