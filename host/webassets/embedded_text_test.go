package webassets

import (
	"os/exec"
	"testing"
)

// Inspect the actual Go embed inventory of ALL platform packages, including
// private embed.FS variables and schema/notice byte embeds outside webassets.
// The checker recursively opens archives and checks exact exception counts.
func TestPlatformEmbeddedText(t *testing.T) {
	cmd := exec.Command("go", "run", "./scripts/split/boundary", "-mode", "embedded")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded text boundary: %v\n%s", err, out)
	}
}
