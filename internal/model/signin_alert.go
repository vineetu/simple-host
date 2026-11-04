package model

type SignInAlertTarget struct {
	Email  string
	Handle string
	On     bool
	// Event is an account an organiser made for an event
	// (users.event_account); those get no alerts.
	Event bool
}
