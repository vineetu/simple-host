package edition

// NotificationPolicy is the notification policy (which events exist and how they are scoped).
type NotificationPolicy struct{}

func notificationsFor(k Kind) NotificationPolicy {
	return NotificationPolicy{}
}
