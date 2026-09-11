// Package capacity answers the only sizing question an event organiser has:
// how many people fit on the server they just created, and how big a site each
// of them may publish.
//
// The two numbers are one number. Disk is the constraint, and every other limit
// on the instance is downstream of it, so picking a per-site cap and picking a
// headcount are the same decision made from two ends. This package does that
// arithmetic in one place so the setup page, the admin API and the organiser's
// agent all quote the same figure.
//
// The arithmetic is deliberately pessimistic. It assumes every participant
// fills their cap, because a limit that only holds when people are modest is
// not a limit.
package capacity

import (
	"fmt"
	"syscall"
)

const (
	// MinSiteBytes is the smallest cap worth offering. A page with a stylesheet,
	// a script and a couple of images fits inside 1 MB with room to spare, which
	// is why the hackathon guidance is that 1 MB is plenty.
	MinSiteBytes int64 = 1 << 20
	// MaxSiteBytes matches the largest archive the extractor will ever accept.
	// Offering a cap above it would be a number the server could not honour.
	MaxSiteBytes int64 = 500 << 20

	// DefaultSitesPerPerson is what one participant realistically publishes:
	// the entry, plus a couple of tries that did not become the entry.
	DefaultSitesPerPerson = 3

	// DefaultPeople is the event size assumed when the organiser has not counted
	// heads yet. Sizing to the smallest cap instead would maximise the headline
	// number and hand every participant a 1 MB quota they did not ask for.
	DefaultPeople = 100

	// UnboundedHistoryEstimate is the deploy history assumed when an instance
	// keeps every version. There is no true answer, so this is a working figure
	// for an agent-driven afternoon; pretending the number is 1 would be worse.
	//
	// It exists because a site on disk is its cap multiplied by its retained
	// history plus the live copy — the single fact that makes naive "disk
	// divided by cap" arithmetic wrong, usually by an order of magnitude. An
	// instance that keeps a bounded number of versions passes that number in
	// and gets real arithmetic instead of this estimate.
	UnboundedHistoryEstimate = 10

	// reserveFloor is held back for Postgres, access logs, the container images
	// and the temporary copy every deploy makes while promoting a version.
	reserveFloor int64 = 2 << 30
	// reserveFraction: a fifth of the disk, when a fifth is more than the floor.
	reserveFraction = 5
)

// rungs are the per-site caps this package will choose between. Sizing to a
// round number an organiser can repeat out loud beats sizing to 6.4 MB.
var rungs = []int64{1, 2, 5, 10, 25, 50, 100, 250, 500}

// Plan is a sizing answer: how many people, how much each may publish, and the
// working shown.
type Plan struct {
	// Disk on the filesystem holding the site data.
	DiskBytes      int64 `json:"disk_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
	UsableBytes    int64 `json:"usable_bytes"`

	// The answer. Requested is what the organiser asked for, zero when they did
	// not say; People is what the sizing actually used.
	Requested int   `json:"requested"`
	People    int   `json:"people"`
	SiteMB    int64 `json:"site_mb"`
	MaxPeople int   `json:"max_people"`

	// The assumptions behind it, so the number is checkable rather than magic.
	SitesPerPerson int `json:"sites_per_person"`
	KeptVersions   int `json:"kept_versions"`
	TotalSites     int `json:"total_sites"`

	// Explanation is one sentence an organiser can read without this doc.
	Explanation string `json:"explanation"`
}

// Usable is what may actually be handed out: available space less the headroom
// the instance needs for itself.
func Usable(available int64) int64 {
	if available <= 0 {
		return 0
	}
	reserve := available / reserveFraction
	if reserve < reserveFloor {
		reserve = reserveFloor
	}
	if reserve >= available {
		return 0
	}
	return available - reserve
}

// bytesPerPerson is what one participant costs at a given cap: every site they
// may create, times every version each site keeps, times the cap.
func bytesPerPerson(siteBytes int64, sitesPerPerson, keptVersions int) int64 {
	return siteBytes * int64(sitesPerPerson) * int64(keptVersions+1)
}

// PeopleAt reports how many participants fit at a given per-site cap.
func PeopleAt(usable, siteBytes int64, sitesPerPerson, keptVersions int) int {
	per := bytesPerPerson(siteBytes, sitesPerPerson, keptVersions)
	if per <= 0 || usable <= 0 {
		return 0
	}
	return int(usable / per)
}

// ForPeople sizes an event: given the space available and how many people are
// coming, it returns the largest round per-site cap that still fits everyone.
//
// people <= 0 means the organiser has not counted heads, and DefaultPeople is
// used instead of the smallest cap: an unknown event is far more likely to be
// a normal one than a record-breaking one.
// keptVersions is how many deploys per site the instance retains; pass 0 or
// less when it retains all of them and UnboundedHistoryEstimate is used.
func ForPeople(diskBytes, availableBytes int64, people, keptVersions int) Plan {
	if keptVersions <= 0 {
		keptVersions = UnboundedHistoryEstimate
	}
	requested := people
	if people <= 0 {
		people = DefaultPeople
	}
	plan := Plan{
		DiskBytes:      diskBytes,
		AvailableBytes: availableBytes,
		UsableBytes:    Usable(availableBytes),
		Requested:      requested,
		People:         people,
		SitesPerPerson: DefaultSitesPerPerson,
		KeptVersions:   keptVersions,
		SiteMB:         MinSiteBytes >> 20,
	}
	// Largest rung that still holds everyone. Falls through to the 1 MB floor
	// when even that does not fit, and Fits then reports that it does not.
	for _, mb := range rungs {
		if PeopleAt(plan.UsableBytes, mb<<20, plan.SitesPerPerson, plan.KeptVersions) >= people {
			plan.SiteMB = mb
		}
	}
	plan.MaxPeople = PeopleAt(plan.UsableBytes, plan.SiteBytes(), plan.SitesPerPerson, plan.KeptVersions)
	plan.TotalSites = plan.MaxPeople * plan.SitesPerPerson
	plan.Explanation = explain(plan)
	return plan
}

func explain(plan Plan) string {
	if plan.MaxPeople == 0 {
		return fmt.Sprintf("This server has %s free, which is not enough headroom to run an event. Use a plan with a bigger disk.",
			human(plan.AvailableBytes))
	}
	// "Budgets", not "holds at least". Only the per-site cap is enforced; the
	// number of sites a participant creates is an assumption. Calling this a
	// floor would be a promise the server cannot keep.
	history := fmt.Sprintf("keeping the last %d deploys of each", plan.KeptVersions)
	if plan.KeptVersions == 1 {
		history = "keeping only the live copy of each"
	}
	return fmt.Sprintf("%s of usable disk budgets %s people at %d MB per site, assuming %d sites each and %s. Only the %d MB is enforced.",
		human(plan.UsableBytes), Thousands(plan.MaxPeople), plan.SiteMB, plan.SitesPerPerson, history, plan.SiteMB)
}

// Fits reports whether a plan actually covers the headcount asked for.
func (p Plan) Fits() bool { return p.MaxPeople > 0 && p.MaxPeople >= p.People }

// SiteBytes is the per-site cap in bytes, clamped to what the extractor accepts.
func (p Plan) SiteBytes() int64 {
	// Clamp the megabytes before shifting, not after. This value can come from
	// a hand-edited instance_config row, and shifting first lets a large enough
	// number wrap to a negative and clamp to the floor instead of the ceiling.
	mb := p.SiteMB
	if mb < MinSiteBytes>>20 {
		return MinSiteBytes
	}
	if mb > MaxSiteBytes>>20 {
		return MaxSiteBytes
	}
	return mb << 20
}

// Disk reports the size and free space of the filesystem holding path.
func Disk(path string) (total, available int64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("read free space on %s: %w", path, err)
	}
	// Bavail, not Bfree: Bfree includes blocks reserved for root, which a
	// service running as a normal user cannot write to and must not promise.
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize), nil
}

func human(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.0f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%d MB", bytes>>20)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// Thousands groups digits so a headcount reads as 25,000 rather than 25000.
// Exported because the setup page quotes the same figures the admin API does,
// and two copies of this would eventually disagree by a comma.
func Thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
