package mcp

import "testing"

func TestStorageRulesSchemas(t *testing.T) {
	for _, tools := range [][]Tool{Tools()} {
		for _, tool := range tools {
			if tool.Name != "storage_set_resource" {
				continue
			}
			t.Run(tool.Name, func(t *testing.T) {
				if err := CheckOutputSchema(tool.InputSchema); err != nil {
					t.Fatal(err)
				}
				target := "site"
				for _, body := range []any{
					map[string]any{"kind": "sqlite", "rules": nil},
					map[string]any{"kind": "sqlite", "rules": map[string]any{"policies": []any{}}, "tables": map[string]any{"notes": map[string]any{"rules": map[string]any{"policies": []any{}}}}},
					map[string]any{"kind": "files", "rules": map[string]any{"policies": []any{}}},
				} {
					if err := ValidateOutput(tool.InputSchema, map[string]any{target: "shop", "name": "notes", "body": body}, nil); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
