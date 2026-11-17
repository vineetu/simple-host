package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// editsUpstream serves one site, blog, live at version 4, and records what
// update_site publishes.
func editsUpstream(t *testing.T, published *map[string]string, mu *sync.Mutex) http.Handler {
	files := map[string]string{
		"index.html": "<h1>Lotus</h1><p>Sunday 11am to 9pm</p><p>Monday 11am to 9pm</p>",
		"menu.html":  "<ul><li>Pad thai</li></ul>",
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sites":
			io.WriteString(w, `[{"name":"blog","url":"https://blog.alice.simple-host.app/","active_version":4,"visibility":"public"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sites/blog":
			io.WriteString(w, `{"name":"blog","url":"https://blog.alice.simple-host.app/","active_version":4,"visibility":"public"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/sites/blog/versions/4/files/"):
			body, ok := files[strings.TrimPrefix(r.URL.Path, "/v1/sites/blog/versions/4/files/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":"file not found","code":"file_not_found"}`)
				return
			}
			io.WriteString(w, body)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sites/blog/files":
			if r.URL.Query().Get("keep") != "1" {
				t.Error("an edit published without keeping the other files")
			}
			var payload struct {
				Files map[string]string `json:"files"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			*published = payload.Files
			mu.Unlock()
			io.WriteString(w, `{"name":"blog","url":"https://blog.alice.simple-host.app/","active_version":5,"visibility":"public"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"not found"}`)
		}
	})
}

func TestUpdateSiteEdits(t *testing.T) {
	var mu sync.Mutex
	var published map[string]string
	s := newTestServer(editsUpstream(t, &published, &mu))
	edit := func(edits ...map[string]any) (string, map[string]any, bool) {
		list := make([]any, len(edits))
		for i, e := range edits {
			list[i] = e
		}
		mu.Lock()
		published = nil
		mu.Unlock()
		return resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "edits": list}), nil, true))
	}

	text, out, isErr := edit(map[string]any{"path": "index.html", "find": "Sunday 11am to 9pm", "replace": "Sunday 10am to 3pm"})
	if isErr {
		t.Fatal("a unique edit failed")
	}
	if got := published["index.html"]; got != "<h1>Lotus</h1><p>Sunday 10am to 3pm</p><p>Monday 11am to 9pm</p>" {
		t.Fatalf("published %q", got)
	}
	if len(published) != 1 {
		t.Errorf("published %d files; only the edited one should be sent", len(published))
	}
	if !strings.Contains(text, "Edited index.html") || out["edited_files"] != nil {
		t.Errorf("edit result: %s %v", text, out)
	}

	// A stale client schema sends the list as a JSON string.
	if text, _, isErr := resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog",
		"edits": `[{"path":"index.html","find":"Monday 11am","replace":"Monday 9am"}]`}), nil, true)); isErr || !strings.Contains(published["index.html"], "Monday 9am") {
		t.Errorf("edits as a JSON string failed: %s", text)
	}
	if _, out, isErr := resultOf(t, send(t, s, toolCall("read_site_file", map[string]any{"site": "blog", "paths": `["menu.html"]`}), nil, true)); isErr || len(out["files"].([]any)) != 1 {
		t.Error("paths as a JSON string failed")
	}

	// Two edits to one file apply in order to the same text.
	if _, _, isErr := edit(
		map[string]any{"path": "index.html", "find": "Sunday 11am", "replace": "Sunday 10am"},
		map[string]any{"path": "index.html", "find": "Sunday 10am to 9pm", "replace": "Sunday 10am to 3pm"},
	); isErr || !strings.Contains(published["index.html"], "Sunday 10am to 3pm") {
		t.Error("chained edits did not apply in order")
	}

	for _, tc := range []struct {
		name string
		edit map[string]any
		want string
	}{
		{"missing", map[string]any{"path": "index.html", "find": "Tuesday", "replace": "x"}, "is not in index.html"},
		{"repeated", map[string]any{"path": "index.html", "find": "11am to 9pm", "replace": "x"}, "occurs 2 times"},
		{"no file", map[string]any{"path": "gone.html", "find": "a", "replace": "b"}, "could not read gone.html"},
		{"empty find", map[string]any{"path": "index.html", "find": "", "replace": "b"}, "non-empty find"},
	} {
		text, _, isErr := edit(tc.edit)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s: isErr=%v text=%s", tc.name, isErr, text)
		}
		if published != nil {
			t.Errorf("%s: published despite the error", tc.name)
		}
	}

	text, _, isErr = resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog",
		"files": map[string]any{"index.html": "x"}, "edits": []any{map[string]any{"path": "index.html", "find": "Lotus", "replace": "y"}}}), nil, true))
	if !isErr || !strings.Contains(text, "both files and edits") {
		t.Errorf("a path in files and edits was accepted: %s", text)
	}
	text, _, isErr = resultOf(t, send(t, s, toolCall("create_site", map[string]any{"site": "new",
		"files": map[string]any{"index.html": "x"}, "edits": []any{map[string]any{"path": "index.html", "find": "a", "replace": "b"}}}), nil, true))
	if !isErr {
		t.Errorf("create_site accepted edits: %s", text)
	}
}

func TestReadSiteFilePaths(t *testing.T) {
	var mu sync.Mutex
	var published map[string]string
	s := newTestServer(editsUpstream(t, &published, &mu))
	text, out, isErr := resultOf(t, send(t, s, toolCall("read_site_file", map[string]any{"site": "blog", "paths": []any{"index.html", "gone.html", "menu.html"}}), nil, true))
	if isErr {
		t.Fatalf("paths read failed: %s", text)
	}
	files, _ := out["files"].([]any)
	if len(files) != 3 || out["version"] != float64(4) {
		t.Fatalf("got %v", out)
	}
	if files[1].(map[string]any)["error"] == nil || files[2].(map[string]any)["content"] != "<ul><li>Pad thai</li></ul>" {
		t.Errorf("a missing file hid the others: %v", files)
	}
	if !strings.Contains(text, "=== menu.html ===") {
		t.Errorf("text result lacks file headers: %s", text)
	}
	for _, args := range []map[string]any{
		{"site": "blog", "path": "index.html", "paths": []any{"menu.html"}},
		{"site": "blog", "paths": []any{}},
		{"site": "blog"},
		{"site": "blog", "paths": []any{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}},
	} {
		if _, _, isErr := resultOf(t, send(t, s, toolCall("read_site_file", args), nil, true)); !isErr {
			t.Errorf("accepted %v", args)
		}
	}
}
