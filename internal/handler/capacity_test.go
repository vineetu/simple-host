package handler

import (
	"testing"

	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/tarball"
)

func TestSetSiteLimitMovesBothCaps(t *testing.T) {
	before := maxSiteArchiveSize
	defer func() { maxSiteArchiveSize = before; tarball.SetSiteLimit(100 << 20) }()

	SetSiteLimit(4 << 20)
	if SiteLimit() != 4<<20 {
		t.Errorf("SiteLimit() = %d, want %d", SiteLimit(), 4<<20)
	}
	// The extractor is the half that actually matters: the upload cap bounds
	// compressed bytes, and a tarball of zeroes compresses a thousand to one.
	// A 4 MB instance whose extractor still allowed 500 MB would accept a 4 MB
	// upload that lands as half a gigabyte of files.
	if !tarball.SiteLimitIs(4 << 20) {
		t.Error("SetSiteLimit did not lower the extractor's caps")
	}

	SetSiteLimit(0)
	if SiteLimit() != 4<<20 {
		t.Errorf("SetSiteLimit(0) changed the cap to %d", SiteLimit())
	}
}

func TestPersistedLimitRoundTripsThroughThePlan(t *testing.T) {
	// What setup writes is megabytes; what the server enforces is bytes, and
	// the conversion is clamped at both ends so a hand-edited row cannot set a
	// zero-byte or half-gigabyte cap on a box that cannot honour it.
	for mb, want := range map[int64]int64{1: 1 << 20, 10: 10 << 20, 0: capacity.MinSiteBytes, 99999: capacity.MaxSiteBytes} {
		if got := (capacity.Plan{SiteMB: mb}).SiteBytes(); got != want {
			t.Errorf("SiteMB %d -> %d bytes, want %d", mb, got, want)
		}
	}
}
