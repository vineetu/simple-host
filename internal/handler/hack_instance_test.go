package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	hacktoolkit "github.com/vsriram/simple-host/hack-toolkit"
)

func TestFullHackInstancePresentation(t *testing.T) {
	prevMode, prevChrome, prevBase := hackMode, hackChrome, hackInkBaseURL
	SetHackMode(true)
	SetHackChrome(true)
	SetHackInstanceURL("http://localhost:18480")
	t.Cleanup(func() { SetHackMode(prevMode); SetHackChrome(prevChrome); SetHackInstanceURL(prevBase) })
	mux := http.NewServeMux()
	RegisterHackHome(mux)
	for _, path := range []string{"/", "/get-started"} {
		rec := get(t, mux, "localhost:18480", path)
		body := rec.Body.String()
		if rec.Code != 200 || strings.Contains(body, "simple-hack.app") || strings.Contains(body, "fonts.googleapis.com") {
			t.Fatalf("instance page %s has hosted assets/links", path)
		}
		if strings.Count(body, `class="sh-header sh-hack"`) != 1 || strings.Count(body, `class="sh-footer"`) != 1 {
			t.Fatalf("%s navigation differs", path)
		}
	}
	for _, name := range hackSkillNames {
		raw, err := hacktoolkit.Skills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		body := string(skillServedText(name+"/SKILL.md", raw))
		if strings.Contains(body, "simple-hack.app") || !strings.Contains(body, "localhost:18480") {
			t.Fatalf("skill %s points elsewhere", name)
		}
	}
	rec := get(t, mux, "localhost:18480", "/simple-hack-skills-only-0.2.7.zip")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".md") && !strings.HasSuffix(f.Name, ".json") && !strings.HasSuffix(f.Name, ".yaml") {
			continue
		}
		src, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("simple-hack.app")) {
			t.Fatalf("instance plugin file %s points to hosted service", f.Name)
		}
	}
	SetHackMode(false)
	if string(hackInstanceText([]byte("https://simple-hack.app"))) != "https://simple-hack.app" {
		t.Fatal("Host text changed")
	}
}
