package web

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The helper's install command fetches the installer from the Release the
// installer pins, by that Release's commit, and checks its sha256 before
// running it. The tag, the commit and the hash agree, and once install.sh's
// Release is tagged the page pins it (bumping VERSION in install.sh without
// setup.js would hand out an older installer).
func TestSetupHelperInstallerRelease(t *testing.T) {
	js, err := StaticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := os.ReadFile("../../deploy/install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	page := regexp.MustCompile(`var INSTALLER_RELEASE = '(v[0-9.]+)';`).FindSubmatch(js)
	commit := regexp.MustCompile(`var INSTALLER_COMMIT = '([0-9a-f]{40})';`).FindSubmatch(js)
	sum := regexp.MustCompile(`var INSTALLER_SHA256 = '([0-9a-f]{64})';`).FindSubmatch(js)
	pinned := regexp.MustCompile(`(?m)^VERSION="(v[0-9.]+)"$`).FindSubmatch(sh)
	if page == nil || commit == nil || sum == nil || pinned == nil {
		t.Fatalf("pin not found: setup.js release %q commit %q sha256 %q, install.sh %q", page, commit, sum, pinned)
	}
	if !strings.Contains(string(js), "'https://raw.githubusercontent.com/vineetu/simple-host/' + INSTALLER_COMMIT + '/deploy/install/install.sh'") {
		t.Error("the install command must fetch install.sh by the pinned commit, not a tag or a branch")
	}
	if !strings.Contains(string(js), `' -o "$f" && printf \'%s  %s\\n\' ' + INSTALLER_SHA256 + ' "$f" | sha256sum -c --quiet - && sudo bash "$f"'`) {
		t.Error("the install command must check install.sh's sha256 before running it")
	}
	// Against the repository, when its history is here: the Release's tag
	// points at the pinned commit, and install.sh there has the pinned hash
	// and names the Release.
	git := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = "../.."
		return cmd.Output()
	}
	if _, err := git("cat-file", "-e", string(commit[1])+"^{commit}"); err != nil {
		if string(page[1]) != string(pinned[1]) {
			t.Errorf("setup.js INSTALLER_RELEASE = %s, install.sh pins %s", page[1], pinned[1])
		}
		t.Skipf("commit %s is not in this checkout's history; hash not checked", commit[1])
	}
	// While a new Release is prepared (VERSION bumped, not yet tagged) the
	// page keeps the last tagged one; once the tag exists, the page pins it.
	if string(page[1]) != string(pinned[1]) {
		if _, err := git("rev-parse", "-q", "--verify", string(pinned[1])+"^{commit}"); err == nil {
			t.Errorf("install.sh is release %s, which is tagged; setup.js still pins %s", pinned[1], page[1])
		}
	}
	if tag, err := git("rev-parse", "-q", "--verify", string(page[1])+"^{commit}"); err == nil && strings.TrimSpace(string(tag)) != string(commit[1]) {
		t.Errorf("tag %s is commit %s, setup.js pins %s", page[1], strings.TrimSpace(string(tag)), commit[1])
	}
	at, err := git("show", string(commit[1])+":deploy/install/install.sh")
	if err != nil {
		t.Fatalf("install.sh at %s: %v", commit[1], err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(at)); got != string(sum[1]) {
		t.Errorf("install.sh at %s has sha256 %s, setup.js pins %s", commit[1], got, sum[1])
	}
	if v := regexp.MustCompile(`(?m)^VERSION="(v[0-9.]+)"$`).FindSubmatch(at); v == nil || string(v[1]) != string(page[1]) {
		t.Errorf("install.sh at %s does not name release %s", commit[1], page[1])
	}
}

// The setup page pins the chart Release; the browser tests render its actual
// generated values against that chart and check both installation commands.
func TestSetupHelperChartRelease(t *testing.T) {
	js, err := StaticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`var ENT_CHART_VERSION = '([0-9]+\.[0-9]+\.[0-9]+)';`).FindSubmatch(js)
	if version == nil {
		t.Fatal("setup page must pin a Helm chart version")
	}
	if !strings.Contains(string(js), "oci://ghcr.io/vineetu/charts/simple-host-enterprise") {
		t.Fatal("setup page must use the published Enterprise chart")
	}
	for _, old := range []string{"ENT_CLOUD_REF", "ENT_APPLY_SHA256", "ALL_CLOUDS"} {
		if strings.Contains(string(js), old) {
			t.Errorf("obsolete cluster provisioning remains: %s", old)
		}
	}
	repo := os.Getenv("SH_ENTERPRISE_REPO")
	if repo == "" {
		return
	}
	chart, err := os.ReadFile(repo + "/deploy/helm/simple-host-enterprise/Chart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	actual := regexp.MustCompile(`(?m)^version: ([0-9.]+)$`).FindSubmatch(chart)
	if actual == nil || string(actual[1]) != string(version[1]) {
		t.Errorf("page pins chart %s, local chart has %q", version[1], actual)
	}
}
