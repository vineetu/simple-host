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

// Edition holds immutable instance policy.
type Edition struct {
	kind          Kind
	routes        RoutePolicy
	presentation  PresentationPolicy
	identity      IdentityPolicy
	storage       StoragePolicy
	notifications NotificationPolicy
}

// New derives instance policy from configuration.
func New(cfg config.Config) *Edition {
	if cfg.Events == "hosted" {
		return ForKind(Hack)
	}
	return ForKind(Host)
}

// ForKind constructs policy for the given edition. Each policy is built in
// its own file (routes.go, presentation.go, identity.go, storage.go,
// notifications.go).
func ForKind(k Kind) *Edition {
	return &Edition{
		kind:          k,
		routes:        routesFor(k),
		presentation:  presentationFor(k),
		identity:      identityFor(k),
		storage:       storageFor(k),
		notifications: notificationsFor(k),
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

// Identity returns the account and credential policy.
func (e *Edition) Identity() IdentityPolicy { return e.or().identity }

// Storage returns the site and storage policy.
func (e *Edition) Storage() StoragePolicy { return e.or().storage }

// Notifications returns the notification policy.
func (e *Edition) Notifications() NotificationPolicy { return e.or().notifications }
