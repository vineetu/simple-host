package webassets

import (
	"io/fs"
	"reflect"
	"testing"
)

func TestHostEmbeddedSkills(t *testing.T) {
	entries, err := fs.ReadDir(Host, "skills")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{"connect-domain", "website-deploy", "website-deploy-builder"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("embedded skills = %v, want %v", names, want)
	}
	if _, err := Static.ReadFile("static/hackathons.html"); err == nil {
		t.Fatal("Host embeds the hackathons page")
	}
}
