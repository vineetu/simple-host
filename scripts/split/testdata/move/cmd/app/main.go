package main

import (
	"example.com/fixture/db"
	"example.com/fixture/internal/alpha"
	"fmt"
)

func main() { fmt.Print(alpha.Value(), db.Schema) }
