package handler

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestSkillsBundleExcludesControlPlaneSkills(t *testing.T) {
	// The bundle is what a participant installs. run-hackathon provisions cloud
	// servers with the operator's own credentials; it shipped to every
	// participant for months, and the Get Started page told them to expect
	// three folders while four arrived.
	data, err := buildSkillsZip()
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
