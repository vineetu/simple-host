package mcp_test

import (
	"github.com/vsriram/simple-host/internal/mcp"
	hackmcp "github.com/vsriram/simple-host/products/hack/mcp"
)

// See hack_product_test.go: the in-package tests read the product from here
// until D3-S1c.
func init() { mcp.InstallHackProductForTest(hackmcp.Contribution, hackmcp.HackPageRecipe) }
