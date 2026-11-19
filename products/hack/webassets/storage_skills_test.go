package webassets

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The toolkit's copies of the storage skills give the same access advice as
// the platform's (host/webassets has the same check for its own copies).
// Recipe topic names are stable. Check access advice, rather than banning words
// such as "records" that are still valid recipe topics.
func TestToolkitStorageSkillsUseRules(t *testing.T) {
	roots := []string{
		"hack-toolkit/skills/website-deploy",
		"hack-toolkit/skills/website-deploy-builder",
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
		"hack-toolkit/skills/run-hackathon/SKILL.md",
	)
	choice := regexp.MustCompile("(?i)(?:presets?\\s+`(?:public|inbox|wall|comments|records|personal|board|private|custom)`|`(?:public|inbox|wall|comments|records|personal|board|private|custom)`\\s+preset|\"preset\"\\s*:)")
	legacyChoice := regexp.MustCompile("(?i)(?:\\bread[\"`]?\\s*[:=]\\s*[\"`\\\\]*(?:own|owner|anyone|signed-in)\\b|whole.resource.{0,40}polic(?:y|ies)|(?:independent|separate|resource.wide).{0,40}read.{0,10}write.{0,10}polic(?:y|ies)|`read`/`write` policy)")
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, paragraph := range strings.Split(string(data), "\n\n") {
				if choice.MatchString(paragraph) {
					t.Errorf("skill still offers a preset as an access choice:\n%s", paragraph)
				}
				if legacyChoice.MatchString(paragraph) {
					t.Errorf("skill still recommends legacy access fields:\n%s", paragraph)
				}
			}
		})
	}
	for _, path := range []string{
		"hack-toolkit/skills/website-deploy/references/storage.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `{"kind":"files","site_passcode":"inherit","rules"`) {
			t.Errorf("%s: file-bucket rules example missing", path)
		}
		if !strings.Contains(text, "server is older and its description says what to") {
			t.Errorf("%s: older-server guidance missing", path)
		}
	}
}
