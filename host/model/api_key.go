package model

import "time"

type APIKey struct {
	ID         string
	Hash       string
	Name       string
	Last4      string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	IdleFrom   time.Time
}
