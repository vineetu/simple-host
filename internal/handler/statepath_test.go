package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitURLPath(t *testing.T) {
	ok := func(in string, want ...string) {
		t.Helper()
		got, err := splitURLPath(in)
		if err != nil {
			t.Fatalf("splitURLPath(%q): unexpected err %v", in, err)
		}
		if len(got) != len(want) {
			t.Fatalf("splitURLPath(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("splitURLPath(%q) = %v, want %v", in, got, want)
			}
		}
	}
	bad := func(in string, want error) {
		t.Helper()
		got, err := splitURLPath(in)
		if !errors.Is(err, want) {
			t.Fatalf("splitURLPath(%q) = (%v, %v), want error %v", in, got, err, want)
		}
	}

	ok("a", "a")
	ok("a/b", "a", "b")
	ok("a/b/c", "a", "b", "c")
	ok("items/0", "items", "0")
	ok("dotted.key/x", "dotted.key", "x")

	bad("", errEmptyStatePath)
	bad("/a", errEmptySegment)
	bad("a/", errEmptySegment)
	bad("a//b", errEmptySegment)
	bad("/", errEmptySegment)

	// 32 segments is the cap; 33 is rejected.
	var thirtyTwo []string
	for i := 0; i < maxStatePathDepth; i++ {
		thirtyTwo = append(thirtyTwo, "x")
	}
	got, err := splitURLPath(strings.Join(thirtyTwo, "/"))
	if err != nil {
		t.Fatalf("32 segments should be allowed: %v", err)
	}
	if len(got) != maxStatePathDepth {
		t.Fatalf("got %d segments, want %d", len(got), maxStatePathDepth)
	}
	thirtyThree := append(thirtyTwo, "y")
	if _, err := splitURLPath(strings.Join(thirtyThree, "/")); !errors.Is(err, errPathTooDeep) {
		t.Fatalf("33 segments: got %v, want %v", err, errPathTooDeep)
	}
}

func TestParseArrayIndex(t *testing.T) {
	if n, ok := parseArrayIndex("0"); !ok || n != 0 {
		t.Fatalf("0: got (%d, %v)", n, ok)
	}
	if n, ok := parseArrayIndex("12"); !ok || n != 12 {
		t.Fatalf("12: got (%d, %v)", n, ok)
	}
	for _, s := range []string{"", "-1", "+1", "01", "00", "1e2", "1.0", "foo"} {
		if n, ok := parseArrayIndex(s); ok {
			t.Fatalf("%q parsed as index %d", s, n)
		}
	}
}

func TestGetAtPath(t *testing.T) {
	root := mustJSON(t, `{"a":{"b":{"c":1},"keep":true},"items":["x","y"],"n":null}`)

	eq := func(path, want string) {
		t.Helper()
		got, err := marshalStateJSON(getAtPath(root, strings.Split(path, "/")))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("get %s = %s, want %s", path, got, want)
		}
	}
	eq("a/b", `{"c":1}`)
	eq("a/b/c", `1`)
	eq("items/0", `"x"`)
	eq("items/1", `"y"`)
	eq("items/2", `null`)
	eq("does/not/exist", `null`)
	eq("a/nope", `null`)
	eq("n", `null`)
	eq("items/foo", `null`)
}

func TestSetAtPathCreatesIntermediates(t *testing.T) {
	// The case jsonb_set(create_missing:=true) gets wrong: missing parents.
	got, err := setAtPath(nil, []string{"a", "b", "c"}, 1.0)
	if err != nil {
		t.Fatalf("create from null: %v", err)
	}
	assertJSON(t, got, `{"a":{"b":{"c":1}}}`)

	root := mustJSON(t, `{"keep":true}`)
	got, err = setAtPath(root, []string{"a", "b"}, "hi")
	if err != nil {
		t.Fatalf("create under existing root: %v", err)
	}
	assertJSON(t, got, `{"a":{"b":"hi"},"keep":true}`)

	root = mustJSON(t, `{"a":{"z":1}}`)
	got, err = setAtPath(root, []string{"a", "b", "c"}, 3.0)
	if err != nil {
		t.Fatalf("create sibling branch: %v", err)
	}
	assertJSON(t, got, `{"a":{"b":{"c":3},"z":1}}`)
}

func TestSetAtPathDoesNotCreateArrays(t *testing.T) {
	_, err := setAtPath(nil, []string{"items", "0"}, 1.0)
	if err == nil {
		t.Fatal("PUT items/0 on missing items should fail")
	}
	if !strings.Contains(err.Error(), "never create arrays") {
		t.Fatalf("error should mention arrays, got %v", err)
	}

	_, err = setAtPath(nil, []string{"0"}, "x")
	if err == nil {
		t.Fatal("PUT /0 on null root should fail")
	}

	root := mustJSON(t, `{"other":1}`)
	_, err = setAtPath(root, []string{"items", "0"}, 1.0)
	if err == nil {
		t.Fatal("PUT items/0 when items is missing should fail")
	}

	// Existing array: integer segment is an index.
	root = mustJSON(t, `{"items":["a","b"]}`)
	got, err := setAtPath(root, []string{"items", "0"}, "z")
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"items":["z","b"]}`)

	// Out of range.
	root = mustJSON(t, `{"items":["a"]}`)
	if _, err := setAtPath(root, []string{"items", "1"}, "z"); err == nil {
		t.Fatal("out-of-range index should fail")
	}

	// Parent exists as object: "0" is a key, not an implicit array.
	root = mustJSON(t, `{"items":{}}`)
	got, err = setAtPath(root, []string{"items", "0"}, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"items":{"0":1}}`)

	// Cannot descend through a scalar.
	root = mustJSON(t, `{"a":5}`)
	if _, err := setAtPath(root, []string{"a", "b"}, 1.0); err == nil {
		t.Fatal("descend through scalar should fail")
	}
}

func TestDeleteAtPathKeepsSiblings(t *testing.T) {
	root := mustJSON(t, `{"a":{"b":1,"c":2},"keep":true}`)
	got, err := deleteAtPath(root, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"a":{"c":2},"keep":true}`)

	root = mustJSON(t, `{"items":["x","y","z"]}`)
	got, err = deleteAtPath(root, []string{"items", "1"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"items":["x","z"]}`)

	// Missing path is a no-op.
	root = mustJSON(t, `{"a":1}`)
	got, err = deleteAtPath(root, []string{"nope", "x"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, got, `{"a":1}`)
}

func TestServeMuxPathWildcardPrecedence(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("GET /v1/sites/{sitename}/state", func(w http.ResponseWriter, r *http.Request) {
		got = "EXACT"
	})
	mux.HandleFunc("GET /v1/sites/{sitename}/state/{path...}", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("path")
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sites/foo/state/a/b", nil))
	if got != "a/b" {
		t.Fatalf("path wildcard: got %q, want a/b", got)
	}
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sites/foo/state", nil))
	if got != "EXACT" {
		t.Fatalf("exact /state: got %q, want EXACT", got)
	}
}

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	v, err := parseStateJSON(json.RawMessage(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func assertJSON(t *testing.T, got any, want string) {
	t.Helper()
	b, err := marshalStateJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("got %s, want %s", gb, wb)
	}
}
