package pages

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/webassets"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// Each place where the platform's setup.js takes Simple Hack's choice exists
// exactly once in the platform file. A release that edits setup.js and drops
// or doubles one fails here by name, not through a changed checksum.
func TestSetupAnchorsAppearOnceInPlatformScript(t *testing.T) {
	shared, err := webassets.Static.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	anchors := []string{setupProductAnchor}
	for _, in := range setupInsertions {
		anchors = append(anchors, in[0])
	}
	if len(anchors) != 9 {
		t.Fatalf("%d anchors, want 9", len(anchors))
	}
	for _, anchor := range anchors {
		if n := bytes.Count(shared, []byte(anchor)); n != 1 {
			t.Errorf("anchor %q appears %d times in the platform setup.js, want 1", anchor, n)
		}
	}
	product, err := hackassets.Static.ReadFile("static/setup/setup-hack.js")
	if err != nil {
		t.Fatal(err)
	}
	out, err := buildSetupJS(shared, product)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("/*sh:product-")) || !bytes.Contains(out, product) {
		t.Error("built setup script keeps a marker or lacks the product's part")
	}
}

func TestSetupScriptRefusesADriftedAnchor(t *testing.T) {
	shared, err := webassets.Static.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, new string }{
		{"missing", "    /*sh:product-choice*/\n", ""},
		{"repeated", "    /*sh:product-choice*/\n", "    /*sh:product-choice*/\n    /*sh:product-choice*/\n"},
		{"product part missing", "  /*sh:product-setup*/\n", ""},
	} {
		drifted := bytes.Replace(shared, []byte(tc.old), []byte(tc.new), 1)
		if bytes.Equal(drifted, shared) {
			t.Fatalf("%s: fixture did not change the script", tc.name)
		}
		if out, err := buildSetupJS(drifted, []byte("x")); err == nil || out != nil || !strings.Contains(err.Error(), "anchor") {
			t.Errorf("%s: want an anchor error, got %v", tc.name, err)
		}
	}
}
