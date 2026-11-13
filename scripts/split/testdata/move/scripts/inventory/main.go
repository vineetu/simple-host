package main

import (
	"os"
	"path/filepath"
	"strings"
)

func main() {
	roots := []string{}
	for _, dir := range []string{"cmd", "internal", "products"} {
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			panic(err)
		}
		roots = append(roots, dir)
	}
	for key := range dynamic {
		path := strings.Split(key, ":")[0]
		found := false
		for _, dir := range roots {
			if strings.HasPrefix(filepath.ToSlash(path), dir+"/") {
				found = true
			}
		}
		if !found {
			panic("key outside discovery roots")
		}
	}
	for key := range dynamic {
		path := strings.Split(key, ":")[0]
		if _, err := os.Stat(path); err != nil {
			panic(err)
		}
		if err := os.WriteFile("inventory.txt", []byte(path+"\n"), 0644); err != nil {
			panic(err)
		}
	}
}
