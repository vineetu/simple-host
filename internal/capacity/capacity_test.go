package capacity

import "testing"

const gb = int64(1) << 30

func TestUsableHoldsBackHeadroom(t *testing.T) {
	// A fifth, once a fifth clears the 2 GB floor.
	if got, want := Usable(100*gb), 80*gb; got != want {
		t.Errorf("Usable(100GB) = %d, want %d", got, want)
	}
	// The floor wins on small disks: 5 GB free yields 3 GB, not 4 GB.
	if got, want := Usable(5*gb), 3*gb; got != want {
		t.Errorf("Usable(5GB) = %d, want %d", got, want)
	}
	// A disk with less free space than the floor hands out nothing at all
	// rather than a negative number that would size an event for -3 people.
	for _, free := range []int64{0, -1, gb, 2 * gb} {
		if got := Usable(free); got != 0 {
			t.Errorf("Usable(%d) = %d, want 0", free, got)
		}
	}
}

func TestDeployHistoryIsCounted(t *testing.T) {
	// The trap this package exists to avoid: usable/cap is not the answer,
	// because a site on disk is its cap times its version history plus the
	// live copy. At the defaults that is 3 sites x 11 copies = 33x per person.
	usable := 33 * gb
	if got, want := PeopleAt(usable, 1<<20, DefaultSitesPerPerson, DefaultKeptVersions), 1024; got != want {
		t.Errorf("PeopleAt(33GB, 1MB) = %d, want %d", got, want)
	}
	// Naive arithmetic would have said 33,792 for the same disk.
	if naive := usable / (1 << 20); naive <= 33000 {
		t.Fatalf("guard is wrong: naive figure %d", naive)
	}
}

func TestForPeoplePicksTheLargestCapThatFits(t *testing.T) {
	// 100 GB disk, 80 GB usable, 100 people: each person costs cap x 33.
	// 25 MB x 33 x 100 = 82.5 GB, too much. 10 MB x 33 x 100 = 33 GB, fits.
	plan := ForPeople(100*gb, 100*gb, 100)
	if plan.SiteMB != 10 {
		t.Errorf("SiteMB = %d, want 10", plan.SiteMB)
	}
	if !plan.Fits() {
		t.Errorf("plan should fit 100 people: %+v", plan)
	}
	if plan.MaxPeople < 100 {
		t.Errorf("MaxPeople = %d, want at least 100", plan.MaxPeople)
	}
}

func TestForPeopleWithoutAHeadcountSizesForANormalEvent(t *testing.T) {
	plan := ForPeople(25*gb, 25*gb, 0)
	if plan.Requested != 0 || plan.People != DefaultPeople {
		t.Errorf("Requested = %d, People = %d; want 0 and %d", plan.Requested, plan.People, DefaultPeople)
	}
	// Not the 1 MB floor: an unstated headcount must not silently hand every
	// participant the stingiest quota on the ladder.
	if plan.SiteMB <= MinSiteBytes>>20 {
		t.Errorf("SiteMB = %d, want a comfortable cap above the floor", plan.SiteMB)
	}
	if !plan.Fits() || plan.MaxPeople < DefaultPeople {
		t.Errorf("a 25 GB disk should hold a normal event: %+v", plan)
	}
	if plan.TotalSites != plan.MaxPeople*plan.SitesPerPerson {
		t.Errorf("TotalSites = %d, inconsistent with MaxPeople = %d", plan.TotalSites, plan.MaxPeople)
	}
}

func TestAnImpossibleHeadcountSaysSoRatherThanShrinkingSilently(t *testing.T) {
	// More people than the smallest cap can hold. The plan must not claim to
	// fit them; an organiser who is told "fine" here discovers it mid-event.
	plan := ForPeople(25*gb, 25*gb, 1_000_000)
	if plan.SiteMB != MinSiteBytes>>20 {
		t.Errorf("SiteMB = %d, want the floor", plan.SiteMB)
	}
	if plan.Fits() {
		t.Errorf("plan claims to fit a million people on 25 GB: %+v", plan)
	}
}

func TestNoDiskAtAllIsReportedNotDividedBy(t *testing.T) {
	plan := ForPeople(gb, gb, 50)
	if plan.MaxPeople != 0 || plan.Fits() {
		t.Errorf("a 1 GB disk should hold nobody: %+v", plan)
	}
	if plan.Explanation == "" {
		t.Error("an unusable server must explain itself")
	}
}

func TestSiteBytesStaysInsideWhatTheExtractorAccepts(t *testing.T) {
	if got := (Plan{SiteMB: 0}).SiteBytes(); got != MinSiteBytes {
		t.Errorf("SiteBytes() = %d, want the %d floor", got, MinSiteBytes)
	}
	if got := (Plan{SiteMB: 100000}).SiteBytes(); got != MaxSiteBytes {
		t.Errorf("SiteBytes() = %d, want the %d ceiling", got, MaxSiteBytes)
	}
}

func TestDiskReadsARealFilesystem(t *testing.T) {
	total, available, err := Disk(t.TempDir())
	if err != nil {
		t.Fatalf("Disk: %v", err)
	}
	if total <= 0 || available < 0 || available > total {
		t.Errorf("Disk returned total=%d available=%d", total, available)
	}
	if _, _, err := Disk("/definitely/not/a/path"); err == nil {
		t.Error("Disk on a missing path should fail, not report zero")
	}
}

func TestThousandsGroupsDigits(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 25000: "25,000", 1234567: "1,234,567"} {
		if got := Thousands(in); got != want {
			t.Errorf("Thousands(%d) = %q, want %q", in, got, want)
		}
	}
}
