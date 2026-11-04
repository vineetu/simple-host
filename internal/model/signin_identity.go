package model

import "time"

type SignInIdentity struct {
	ID       string
	Provider string
	Email    string
	LinkedAt time.Time
}
