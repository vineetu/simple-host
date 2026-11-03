package edition

// RoutePolicy describes edition-specific routes.
type RoutePolicy struct {
	// HostedEvents enables hosted event routes.
	HostedEvents bool
}

func routesFor(k Kind) RoutePolicy {
	return RoutePolicy{HostedEvents: k == Hack}
}
