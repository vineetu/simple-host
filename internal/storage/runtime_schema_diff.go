package storage

import "sort"

// SchemaTableChanges returns added tables and whether a preset table disappeared.
func SchemaTableChanges(before, after map[string]bool, hasPreset func(string) bool) (bool, []string) {
	var renamed bool
	added := []string{}
	for t := range before {
		if hasPreset(t) && !after[t] {
			renamed = true
		}
	}
	for t := range after {
		if !before[t] {
			added = append(added, t)
		}
	}
	sort.Strings(added)
	return renamed, added
}
