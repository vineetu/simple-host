package edition

// NotificationPolicy describes notification event and access scope.
type NotificationPolicy struct {
	// EventScoped enables Hack event membership and website scoping.
	EventScoped bool
}

func notificationsFor(k Kind) NotificationPolicy {
	return NotificationPolicy{EventScoped: k == Hack}
}
