// Command mcpidentifiers inventories typed references across the MCP product boundary.
package main

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func productFile(name string) bool {
	name = filepath.Base(name)
	return strings.HasPrefix(name, "hack_") || name == "families_hack.go"
}

func main() {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "./internal/mcp")
	if err != nil {
		panic(err)
	}
	if packages.PrintErrors(pkgs) != 0 {
		os.Exit(1)
	}
	p := pkgs[0]
	sections := make([]map[string]bool, 3)
	for i := range sections {
		sections[i] = map[string]bool{}
	}
	symbol := func(obj types.Object) string {
		pos := p.Fset.Position(obj.Pos())
		return fmt.Sprintf("%s | %s | %s", obj.Name(), filepath.Base(pos.Filename), types.ObjectString(obj, func(pkg *types.Package) string { return pkg.Name() }))
	}
	for _, obj := range p.TypesInfo.Defs {
		if obj == nil || obj.Pkg() != p.Types || obj.Parent() != p.Types.Scope() {
			continue
		}
		if (strings.HasPrefix(obj.Name(), "hack") || strings.HasPrefix(obj.Name(), "Hack")) && !productFile(p.Fset.Position(obj.Pos()).Filename) {
			sections[2][symbol(obj)] = true
		}
	}
	for id, obj := range p.TypesInfo.Uses {
		if obj == nil || obj.Pkg() != p.Types || !obj.Pos().IsValid() {
			continue
		}
		// Local variables do not cross the package-file boundary. Include fields and methods.
		if obj.Parent() != p.Types.Scope() {
			switch o := obj.(type) {
			case *types.Func:
				if sig, ok := o.Type().(*types.Signature); !ok || sig.Recv() == nil {
					continue
				}
			case *types.Var:
				if !o.IsField() {
					continue
				}
			default:
				continue
			}
		}
		from := productFile(p.Fset.Position(id.Pos()).Filename)
		to := productFile(p.Fset.Position(obj.Pos()).Filename)
		if from != to {
			section := 0
			if !from {
				section = 1
			}
			sections[section][symbol(obj)] = true
		}
	}
	fmt.Println("# MCP identifiers (go/types; production files only)")
	fmt.Println("# Regenerate: go run ./scripts/split/mcpidentifiers > scripts/split/reports/mcp-identifiers.txt")
	for i, heading := range []string{"Platform identifiers used by Hack files", "Hack identifiers used by platform files", "Hack identifiers defined in a file not named for Hack"} {
		fmt.Printf("\n## %s\n", heading)
		keys := make([]string, 0, len(sections[i]))
		for key := range sections[i] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Println(key)
		}
	}
}
