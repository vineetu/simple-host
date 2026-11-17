// Package edition defines fixed instance policy. Main builds an Edition once and
// injects it into consumers; dynamic facts such as events and deadlines do not live here.
package edition

import "github.com/vsriram/simple-host/host/config"

// Edition holds immutable instance policy.
type Edition struct {
	routes        RoutePolicy
	presentation  PresentationPolicy
	identity      IdentityPolicy
	storage       StoragePolicy
	notifications NotificationPolicy
	limits        config.Limits
}

// FromPolicies constructs an immutable edition from product-neutral values.
func FromPolicies(routes RoutePolicy, presentation PresentationPolicy, identity IdentityPolicy, storage StoragePolicy, notifications NotificationPolicy, limits config.Limits) *Edition {
	return &Edition{routes: routes, presentation: presentation, identity: identity, storage: storage, notifications: notifications, limits: limits}
}

// Default constructs the platform's policy with the supplied operational limits.
func Default(limits config.Limits) *Edition {
	return FromPolicies(RoutePolicy{}, PresentationPolicy{ProductName: "Simple Host"}, IdentityPolicy{PersonalSites: true}, StoragePolicy{Photos: true, LegacyRoutes: true, FileCaps: true}, NotificationPolicy{}, limits)
}

// Limits returns the instance's operational limits as a value copy.
func (e *Edition) Limits() config.Limits { return e.or().limits }

// host answers for a nil Edition (a bare &SiteHandler{} in a test), so it
// behaves as Simple Host, as the old global's zero value did.
var host = Default(config.DefaultLimits())

func (e *Edition) or() *Edition {
	if e == nil {
		return host
	}
	return e
}

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

// Limits is the operational snapshot supplied to application workflows.
type Limits = config.Limits
