package sites

import (
	"database/sql"
	"sync"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/eventdns"
)

// Service owns site lifecycle operations.
// Its fields are exported so the feature packages under host/sites can run
// their workflows on the same instance (they embed it).
type Service struct {
	Accounts *accountapp.Service

	DB              *sql.DB
	Disk            Disk
	PreviewAccounts map[string]bool
	PreviewTTL      time.Duration
	Deps            Deps
}

// Deps holds collaborators shared by site workflows.
type Deps struct {
	Sitemap SitemapFilter
	// Limits are the instance's limits (ed.Limits()); the account rules in
	// host/access read them.
	Limits edition.Limits
	// RandomHandles: a first deploy is given a random handle (edition identity).
	RandomHandles bool
	// Locks is the instance's per-site upload lock table (LockSiteName).
	Locks *sync.Map
	// Hosts is the instance's addressing and certificate state; nil where a
	// workflow needs none.
	Hosts *Hosts
	// NoVisitorSignIn: this server has no way for a visitor to sign in.
	NoVisitorSignIn bool
	// EventVersionRetention: retention keeps the latest versions below the
	// active one (edition storage).
	EventVersionRetention   bool
	SiteViewersMax          int
	PlatformDomains         []string
	ContentHost, SiteDomain string
	PersonalSitesEnabled    bool
	DefaultRetention        int
	GroupGate               GroupGate
	FileCaps                bool
	CapCutoff               string
	DeployEnabled           bool
	RedirectsMaxRules       int
	MaxFilesPerSite         int
	WebsiteArchiveDefault   int64
	EventDNS                eventdns.Provider
	EventProbe              *eventdns.LiveProbe
	EventNamePeer           *eventdns.NamePeer
	EventDomains            []string
	EventTTL                time.Duration
	EventMaxClaims          int
	DeletedRetention        time.Duration
	IdleArchive             IdleArchiveDeps
}

// New constructs a site lifecycle service using the instance's shared locks.
func New(database *sql.DB, disk Disk, previewAccounts map[string]bool, previewTTL time.Duration, deps Deps) *Service {
	return &Service{Accounts: accountapp.New(database), DB: database, Disk: disk, PreviewAccounts: previewAccounts, PreviewTTL: previewTTL, Deps: deps}
}

// fileBytes is the site's footprint on disk, 0 when it cannot be measured.
func (s *Service) FileBytes(site model.Site) int64 {
	footprint, err := s.Disk.SiteFootprint(site.UserID, site.Name)
	if err != nil {
		return 0
	}
	return footprint.Total()
}
