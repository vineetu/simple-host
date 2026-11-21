package web

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
)

func TestSkillsBundleExcludesControlPlaneSkills(t *testing.T) {
	// The Simple Host bundle contains only its three website skills.
	data, err := NewAssetPipeline(edition.Default(config.DefaultLimits()), "").BuildSkillsZip()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	tops := map[string]bool{}
	for _, f := range reader.File {
		if i := strings.Index(f.Name, "/"); i > 0 {
			tops[f.Name[:i]] = true
		}
	}
	for _, name := range controlPlaneSkills {
		if tops[name] {
			t.Errorf("%q is in the participant bundle", name)
		}
	}
	// And the ones that belong there still do, or the fix broke the product.
	for _, name := range []string{"website-deploy", "website-deploy-builder", "connect-domain"} {
		if !tops[name] {
			t.Errorf("%q is missing from the bundle", name)
		}
	}
}
