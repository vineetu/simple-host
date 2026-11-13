// A tiny checker for the fixture's final layering rules and real Go import edges.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const module = "example.com/fixture/"

func local(path string) string {
	if !strings.HasPrefix(path, module) {
		return ""
	}
	path = strings.TrimPrefix(path, module)
	if strings.HasPrefix(path, "internal/") {
		return strings.TrimPrefix(path, "internal/")
	}
	return "@/" + path
}
func expand(pattern string) []string {
	i := strings.Index(pattern, "{")
	if i < 0 {
		return []string{pattern}
	}
	j := strings.Index(pattern[i:], "}") + i
	var result []string
	for _, part := range strings.Split(pattern[i+1:j], ",") {
		result = append(result, expand(pattern[:i]+part+pattern[j+1:])...)
	}
	return result
}
func match(pattern, name string) bool {
	for _, p := range expand(pattern) {
		if strings.HasPrefix(p, "@/") {
			p = local(module + strings.TrimPrefix(p, "@/"))
		}
		if p == name || (strings.HasSuffix(p, "/**") && (name == strings.TrimSuffix(p, "/**") || strings.HasPrefix(name, strings.TrimSuffix(p, "**")))) {
			return true
		}
	}
	return false
}
func main() {
	data, err := os.ReadFile("scripts/layering/rules.txt")
	if err != nil {
		panic(err)
	}
	rules := map[string][]string{}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[final]" || !strings.Contains(line, " -> ") {
			continue
		}
		sides := strings.SplitN(line, " -> ", 2)
		rules[sides[0]] = strings.Fields(sides[1])
	}
	cmd := exec.Command("go", "list", "-json", "./...")
	output, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			panic(err)
		}
		from := local(pkg.ImportPath)
		for _, imp := range pkg.Imports {
			to := local(imp)
			if to == "" {
				continue
			}
			allowed := false
			for pattern, targets := range rules {
				if !match(pattern, from) {
					continue
				}
				for _, target := range targets {
					if match(target, to) {
						allowed = true
					}
				}
			}
			if !allowed {
				panic(fmt.Sprintf("forbidden edge %s -> %s", from, to))
			}
		}
	}
	fmt.Println("layering passed")
}
