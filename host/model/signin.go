package model

import "time"

type SignInAlertTarget struct {
	Email  string
	Handle string
	On     bool
	// Event is an account an organiser made for an event
	// (users.event_account); those get no alerts.
	Event bool
}

type SignInIdentity struct {
	ID       string
	Provider string
	Email    string
	LinkedAt time.Time
}
