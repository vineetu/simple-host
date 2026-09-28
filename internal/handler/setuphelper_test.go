package handler

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// The setup helper is served at /setup with its script, and the small-box
// settings list it reads is the one the code describes.
func TestSetupHelper(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/setup")
	if rec.Code != http.StatusOK {
		t.Fatalf("/setup: status %d", rec.Code)
	}
	body := rec.Body.String()
	assertChrome(t, "/setup", body, "")
	if !strings.Contains(body, `<script src="/setup/setup.js"></script>`) {
		t.Error("/setup does not load its script")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("/setup CSP = %q", csp)
	}
	if rec := get(t, mux, "simple-host.app", "/setup/"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/setup" {
		t.Errorf("/setup/: status %d to %q, want 301 to /setup", rec.Code, rec.Header().Get("Location"))
	}
	if validateHandle("setup") == nil {
		t.Error("the handle \"setup\" must be reserved")
	}
	want, err := config.SettingsJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, mux, "simple-host.app", "/setup/small-box-settings.json").Body.String(); got != string(want) {
		t.Error("static/setup/small-box-settings.json differs from the settings in code: run scripts/sync-settings.sh")
	}
	for _, p := range []string{"/setup/setup.js", "/setup/enterprise-settings.json"} {
		if rec := get(t, mux, "simple-host.app", p); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rec.Code)
		}
	}
}

// The helper's install command fetches the installer from the release the
// installer pins, by that release's commit, and checks its sha256 before
// running it. The tag, the commit and the hash agree, and once install.sh's
// release is tagged the page pins it (bumping VERSION in install.sh without
// setup.js would hand out an older installer).
func TestSetupHelperInstallerRelease(t *testing.T) {
	js, err := staticFiles.ReadFile("static/setup/setup.js")
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
	// Against the repository, when its history is here: the release's tag
	// points at the pinned commit, and install.sh there has the pinned hash
	// and names the release.
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
	// While a new release is prepared (VERSION bumped, not yet tagged) the
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

// The UpCloud button is the referral link, exactly, opened apart from this
// page.
func TestSetupHelperUpCloudReferral(t *testing.T) {
	js, err := staticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "var UPCLOUD_SIGNUP = 'https://signup.upcloud.com/?promo=JF2WCV';") {
		t.Error("UPCLOUD_SIGNUP changed")
	}
}

// The quick path's line runs deploy/terraform/<cloud>/apply.sh from the
// enterprise repo at a pinned commit, checked against its sha256 first. With
// SH_ENTERPRISE_REPO pointing at a checkout of that repo, the pins are checked
// against it.
func TestSetupHelperCloudPins(t *testing.T) {
	js, err := staticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	ref := regexp.MustCompile(`var ENT_CLOUD_REF = '([0-9a-f]{40})';`).FindStringSubmatch(src)
	sums := regexp.MustCompile(`var ENT_APPLY_SHA256 = \{([^}]*)\};`).FindStringSubmatch(src)
	if ref == nil || sums == nil {
		t.Fatalf("pins not found: ENT_CLOUD_REF %q, ENT_APPLY_SHA256 %q", ref, sums)
	}
	pinned := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+): '([^']*)'`).FindAllStringSubmatch(sums[1], -1) {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(m[2]) {
			t.Errorf("ENT_APPLY_SHA256.%s = %q, not a sha256", m[1], m[2])
		}
		pinned[m[1]] = m[2]
	}
	// Every cloud switched on has a pin.
	for _, m := range regexp.MustCompile(`\{ id: '(\w+)', on: true,`).FindAllStringSubmatch(src, -1) {
		if pinned[m[1]] == "" {
			t.Errorf("cloud %s is on but ENT_APPLY_SHA256 has no pin for it", m[1])
		}
	}
	if !strings.Contains(src, `ENT_RAW + ENT_CLOUD_REF + '/deploy/terraform/' + c.id + '/apply.sh -o "$f" && printf \'%s  %s\\n\' ' +`) ||
		!strings.Contains(src, `ENT_APPLY_SHA256[c.id] + ' "$f" | sha256sum -c --quiet - && bash "$f" --ref ' + ENT_CLOUD_REF`) {
		t.Error("the cloud command must fetch apply.sh by the pinned commit and check its sha256 before running it")
	}
	repo := os.Getenv("SH_ENTERPRISE_REPO")
	if repo == "" {
		t.Skip("SH_ENTERPRISE_REPO unset; pins not checked against the enterprise repo")
	}
	for cloud, sum := range pinned {
		out, err := exec.Command("git", "-C", repo, "show", ref[1]+":deploy/terraform/"+cloud+"/apply.sh").Output()
		if err != nil {
			t.Errorf("%s: apply.sh not at %s in %s: %v", cloud, ref[1], repo, err)
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(out)); got != sum {
			t.Errorf("%s: apply.sh at %s has sha256 %s, setup.js pins %s", cloud, ref[1], got, sum)
		}
	}
}

// hcl() writes a Terraform string: quotes and backslashes escaped, and the
// template sequences ${ and %{ doubled so Terraform reads them literally.
func TestSetupHelperHCLEscape(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, _ := staticFiles.ReadFile("static/setup/setup.js")
	m := regexp.MustCompile(`(?s)\n  function hcl\(v\) \{.*?\n  \}\n`).Find(js)
	if m == nil {
		t.Fatal("setup.js lacks function hcl")
	}
	out, err := exec.Command(node, "-e", string(m)+`process.stdout.write(hcl('a${b} %{c} "q" \\ $x %d'));`).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if want := `"a$${b} %%{c} \"q\" \\ $x %d"`; string(out) != want {
		t.Errorf("hcl = %s, want %s", out, want)
	}
}
