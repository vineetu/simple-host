package config

import (
	"strings"
	"testing"
)

func TestSiteOverridesParse(t *testing.T) {
	l, err := LoadLimits(func(k string) string {
		if k == "MAX_SITES_OVERRIDES" {
			return " ChhotaBreak:2000 , ann:1,,bob-2:100000 "
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.MaxSitesOverrides != "ann:1,bob-2:100000,chhotabreak:2000" {
		t.Fatalf("normalised = %q", l.MaxSitesOverrides)
	}
	if got := l.MaxSitesFor("chhotabreak"); got != 2000 {
		t.Fatalf("chhotabreak = %d", got)
	}
	if got := l.MaxSitesFor("someone", "ANN"); got != 1 {
		t.Fatalf("earlier handle ann = %d", got)
	}
	if got := l.MaxSitesFor("someone"); got != l.MaxSitesPerAccount {
		t.Fatalf("no override = %d, want %d", got, l.MaxSitesPerAccount)
	}
	if d := DefaultLimits(); d.MaxSitesOverrides != "" || d.SiteOverrides() != nil || d.MaxSitesFor("chhotabreak") != 100 {
		t.Fatalf("default not empty: %q", d.MaxSitesOverrides)
	}

	for _, bad := range []string{"chhotabreak", "chhotabreak:", ":20", "chhotabreak:0", "chhotabreak:100001",
		"chhotabreak:-5", "chhotabreak:2k", "bad_handle:5", "a:1,a:2", "chhotabreak=2000"} {
		_, err := LoadLimits(func(k string) string {
			if k == "MAX_SITES_OVERRIDES" {
				return bad
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "MAX_SITES_OVERRIDES") {
			t.Errorf("%q: error = %v, want one naming MAX_SITES_OVERRIDES", bad, err)
		}
	}
}

func TestArchiveOverridesParse(t *testing.T) {
	l, err := LoadLimits(func(k string) string {
		if k == "MAX_ARCHIVE_MB_OVERRIDES" {
			return " Vineetu:300 , jot-transcribe:300,,small:1,top:500 "
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.MaxArchiveOverrides != "jot-transcribe:300,small:1,top:500,vineetu:300" {
		t.Fatalf("normalised = %q", l.MaxArchiveOverrides)
	}
	if mb, ok := l.ArchiveOverrideMB("vineetu"); !ok || mb != 300 {
		t.Fatalf("vineetu = %d %v", mb, ok)
	}
	if mb, ok := l.ArchiveOverrideMB("someone", "JOT-TRANSCRIBE"); !ok || mb != 300 {
		t.Fatalf("earlier handle = %d %v", mb, ok)
	}
	if _, ok := l.ArchiveOverrideMB("someone"); ok {
		t.Fatal("no override matched")
	}
	// The two settings are separate lists.
	if l.MaxSitesFor("vineetu") != l.MaxSitesPerAccount || l.SiteOverrides() != nil {
		t.Fatal("an archive override leaked into MAX_SITES_OVERRIDES")
	}
	if d := DefaultLimits(); d.MaxArchiveOverrides != "" || d.ArchiveOverrides() != nil {
		t.Fatalf("default not empty: %q", d.MaxArchiveOverrides)
	}

	for _, bad := range []string{"vineetu", "vineetu:", ":300", "vineetu:0", "vineetu:501",
		"vineetu:-5", "vineetu:300MB", "bad_handle:5", "a:1,a:2", "vineetu=300"} {
		_, err := LoadLimits(func(k string) string {
			if k == "MAX_ARCHIVE_MB_OVERRIDES" {
				return bad
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "MAX_ARCHIVE_MB_OVERRIDES") {
			t.Errorf("%q: error = %v, want one naming MAX_ARCHIVE_MB_OVERRIDES", bad, err)
		}
	}
}
