package wpimport

import (
	"encoding/json"
	"regexp"
	"strings"
)

type block struct {
	Name     string
	Attrs    map[string]any
	Children []*block
	HTML     string
}

var blockComment = regexp.MustCompile(`(?s)<!--\s*(/?)wp:([a-zA-Z0-9_/-]+)\s*(.*?)-->`)

// Text nodes preserve HTML between blocks, including malformed editor markup.
func parseBlocks(s string) []*block {
	root := &block{}
	stack := []*block{root}
	pos := 0
	for _, m := range blockComment.FindAllStringSubmatchIndex(s, -1) {
		cur := stack[len(stack)-1]
		if m[0] > pos {
			cur.Children = append(cur.Children, &block{HTML: s[pos:m[0]]})
		}
		closing := s[m[2]:m[3]] != ""
		name := s[m[4]:m[5]]
		if !strings.Contains(name, "/") {
			name = "core/" + name
		}
		tail := strings.TrimSpace(s[m[6]:m[7]])
		if closing {
			if len(stack) > 1 && stack[len(stack)-1].Name == name {
				stack = stack[:len(stack)-1]
			}
		} else {
			self := strings.HasSuffix(tail, "/")
			tail = strings.TrimSpace(strings.TrimSuffix(tail, "/"))
			n := &block{Name: name, Attrs: map[string]any{}}
			if tail != "" {
				_ = json.Unmarshal([]byte(tail), &n.Attrs)
			}
			cur.Children = append(cur.Children, n)
			if !self {
				stack = append(stack, n)
			}
		}
		pos = m[1]
	}
	if pos < len(s) {
		stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, &block{HTML: s[pos:]})
	}
	return root.Children
}
func attrString(a map[string]any, k string) string {
	v := a[k]
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return jsonNumber(v)
	}
	return ""
}
func jsonNumber(v float64) string { b, _ := json.Marshal(v); return string(b) }
