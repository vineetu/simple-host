package webassets

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Recipe topic names are stable. Check access advice, rather than banning words
// such as "records" that are still valid recipe topics and file-bucket presets.
func TestStorageSkillsUseRulesAndKeepFilePresets(t *testing.T) {
	roots := []string{
		"simple-host-website/skills/website-deploy",
		"simple-host-website/skills/website-deploy-builder",
		"hack-toolkit/skills/website-deploy",
		"hack-toolkit/skills/website-deploy-builder",
		"hack-toolkit/site/skills/website-deploy",
		"hack-toolkit/site/skills/website-deploy-builder",
		"../../openai-plugin/skills/website-deploy",
		"../../openai-plugin/skills/website-deploy-builder",
		"../../plugins/simple-host/skills/website-deploy",
		"../../plugins/simple-host/skills/website-deploy-builder",
		"../../hack-toolkit/skills/website-deploy",
		"../../hack-toolkit/skills/website-deploy-builder",
	}
	var paths []string
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
				paths = append(paths, path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	paths = append(paths,
		"../../openai-plugin/skills/website-deploy/references/storage.md",
		"hack-toolkit/skills/run-hackathon/SKILL.md",
		"hack-toolkit/site/skills/run-hackathon/SKILL.md",
		"../../hack-toolkit/skills/run-hackathon/SKILL.md",
	)
	choice := regexp.MustCompile("(?i)(?:presets?\\s+`(?:public|inbox|wall|comments|records|personal|board|private|custom)`|`(?:public|inbox|wall|comments|records|personal|board|private|custom)`\\s+preset|\"preset\"\\s*:)")
	legacyChoice := regexp.MustCompile("(?i)(?:\\bread[\"`]?\\s*[:=]\\s*[\"`\\\\]*(?:own|owner|anyone|signed-in)\\b|whole.resource.{0,40}polic(?:y|ies)|(?:independent|separate|resource.wide).{0,40}read.{0,10}write.{0,10}polic(?:y|ies)|`read`/`write` policy)")
	rowResource := regexp.MustCompile(`"kind"\s*:\s*"(?:sqlite|kv)"`)
	rowKind := regexp.MustCompile(`(?i)\b(?:sqlite|kv|key.value)\b`)
	fileChoice := regexp.MustCompile(`(?i)\b(?:files|file.bucket|photos)\b`)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, paragraph := range strings.Split(string(data), "\n\n") {
				if choice.MatchString(paragraph) && (!fileChoice.MatchString(paragraph) || rowResource.MatchString(paragraph)) {
					t.Errorf("SQLite/KV skill still offers a preset as an access choice:\n%s", paragraph)
				}
				if legacyChoice.MatchString(paragraph) && (!fileChoice.MatchString(paragraph) || rowResource.MatchString(paragraph) || rowKind.MatchString(paragraph)) {
					t.Errorf("SQLite/KV skill still recommends legacy access fields:\n%s", paragraph)
				}
			}
		})
	}
	for _, path := range []string{
		"simple-host-website/skills/website-deploy/references/storage.md",
		"hack-toolkit/skills/website-deploy/references/storage.md",
		"../../openai-plugin/skills/website-deploy/references/storage.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `{"kind":"files","preset":"wall"`) {
			t.Errorf("%s: file-bucket preset example missing", path)
		}
		if !strings.Contains(text, "server is older and its description says what to") {
			t.Errorf("%s: older-server guidance missing", path)
		}
	}
}
