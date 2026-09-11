package capacity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSite lays out a site the way DiskStorage does: versions plus the live
// copy, all under by-id/<userID>/<siteName>/.
func writeSite(t *testing.T, root, userID, name string, versions map[string]int) {
	t.Helper()
	for version, size := range versions {
		dir := filepath.Join(root, "by-id", userID, name, version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMeasureCountsEveryCopyOfASite(t *testing.T) {
	root := t.TempDir()
	// Versions are full copies, so all of them count. Reporting only the live
	// one would understate a busy site by its entire history — the thing that
	// actually fills a small disk.
	writeSite(t, root, "user-a", "entry", map[string]int{"v1": 1000, "v2": 2000, "current": 2000})
	writeSite(t, root, "user-b", "quiz", map[string]int{"v1": 500, "current": 500})

	usage, err := Measure(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Sites != 2 {
		t.Errorf("Sites = %d, want 2", usage.Sites)
	}
	if usage.SiteBytes != 6000 {
		t.Errorf("SiteBytes = %d, want 6000", usage.SiteBytes)
	}
	if len(usage.Largest) != 2 || usage.Largest[0].Name != "entry" || usage.Largest[0].Bytes != 5000 {
		t.Errorf("Largest = %+v, want entry at 5000 first", usage.Largest)
	}
}

func TestMeasureIgnoresThingsThatAreNotSiteFiles(t *testing.T) {
	root := t.TempDir()
	writeSite(t, root, "user-a", "entry", map[string]int{"current": 100})

	// The symlink farm: handles/<handle> -> ../by-id/<userID>. A symlink reports
	// the length of its target path, so counting it would attribute bytes to a
	// site that has none, and following it would count the same files twice.
	if err := os.MkdirAll(filepath.Join(root, "handles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "by-id", "user-a"), filepath.Join(root, "handles", "ada")); err != nil {
		t.Fatal(err)
	}
	// A stray file directly under a user directory belongs to no site.
	if err := os.WriteFile(filepath.Join(root, "by-id", "user-a", "stray.txt"), make([]byte, 9999), 0o644); err != nil {
		t.Fatal(err)
	}

	usage, err := Measure(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if usage.SiteBytes != 100 {
		t.Errorf("SiteBytes = %d, want 100", usage.SiteBytes)
	}
	if usage.Sites != 1 {
		t.Errorf("Sites = %d, want 1", usage.Sites)
	}
}

func TestMeasureOnAnEmptyInstance(t *testing.T) {
	usage, err := Measure(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Sites != 0 || usage.SiteBytes != 0 {
		t.Errorf("empty instance reports %d sites, %d bytes", usage.Sites, usage.SiteBytes)
	}
	// The filesystem still has to be described, or the admin screen shows
	// nothing at all on a brand-new box.
	if usage.DiskBytes <= 0 {
		t.Errorf("DiskBytes = %d", usage.DiskBytes)
	}
	if usage.Status == "" || usage.Message == "" {
		t.Error("an empty instance should still describe its disk")
	}
}

func TestStatusEscalatesBeforeItIsTooLate(t *testing.T) {
	cases := []struct {
		pct    float64
		status string
	}{{10, "ok"}, {74.9, "ok"}, {75, "filling"}, {89.9, "filling"}, {90, "full"}, {99.9, "full"}}
	for _, c := range cases {
		status, message := describe(Usage{DiskUsedPct: c.pct, DiskBytes: 100 << 30, DiskFreeBytes: 10 << 30})
		if status != c.status {
			t.Errorf("%.1f%% -> %q, want %q", c.pct, status, c.status)
		}
		if message == "" {
			t.Errorf("%.1f%% has no message", c.pct)
		}
		// A warning that does not say what is left is not actionable.
		if c.status != "ok" && !strings.Contains(message, "left") {
			t.Errorf("%.1f%% message does not say what is left: %q", c.pct, message)
		}
	}
}

func TestHumanReadsLikeAnAdminScreen(t *testing.T) {
	for bytes, want := range map[int64]string{
		512: "512 bytes", 25 << 10: "25 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB",
	} {
		if got := Human(bytes); got != want {
			t.Errorf("Human(%d) = %q, want %q", bytes, got, want)
		}
	}
}
