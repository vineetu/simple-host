// Package edition defines fixed instance policy. Main builds an Edition once and
// injects it into consumers; dynamic facts such as events and deadlines do not live here.
package edition

import "github.com/vsriram/simple-host/internal/config"

// Kind identifies the product edition.
type Kind int

const (
	// Host is the website hosting edition.
	Host Kind = iota
	// Hack is the hosted events edition.
	Hack
)

// String returns the edition name.
func (k Kind) String() string {
	if k == Hack {
		return "hack"
	}
	return "host"
}

// RoutePolicy describes edition-specific routes.
type RoutePolicy struct {
	// HostedEvents enables hosted event routes.
	HostedEvents bool
}

// PresentationPolicy describes edition-specific presentation.
type PresentationPolicy struct {
	// ProductName is the public product name.
	ProductName string
	// HackAssets selects Simple Hack presentation and skills.
	HackAssets bool
}

// Edition holds immutable instance policy.
type Edition struct {
	kind         Kind
	routes       RoutePolicy
	presentation PresentationPolicy
}

// New derives instance policy from configuration.
func New(cfg config.Config) *Edition {
	if cfg.Events == "hosted" {
		return ForKind(Hack)
	}
	return ForKind(Host)
}

// ForKind constructs policy for the given edition.
func ForKind(k Kind) *Edition {
	if k == Hack {
		return &Edition{
			kind:         Hack,
			routes:       RoutePolicy{HostedEvents: true},
			presentation: PresentationPolicy{ProductName: "Simple Hack", HackAssets: true},
		}
	}
	return &Edition{
		kind:         Host,
		presentation: PresentationPolicy{ProductName: "Simple Host"},
	}
}

// host answers for a nil Edition (a bare &SiteHandler{} in a test), so it
// behaves as Simple Host, as the old global's zero value did.
var host = ForKind(Host)

func (e *Edition) or() *Edition {
	if e == nil {
		return host
	}
	return e
}

// Kind returns the product edition.
func (e *Edition) Kind() Kind { return e.or().kind }

// Routes returns the route policy.
func (e *Edition) Routes() RoutePolicy { return e.or().routes }

// Presentation returns the presentation policy.
func (e *Edition) Presentation() PresentationPolicy { return e.or().presentation }
