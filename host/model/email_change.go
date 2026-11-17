package model

import "time"

type EmailChange struct {
	NewEmail    string
	CodeHash    string // the code sent to the new address
	OldCodeHash string // the code sent to the current address
	Attempts    int
	ExpiresAt   time.Time
}
type EmailChangeUndo struct {
	UserID    string
	OldEmail  string
	NewEmail  string
	ChangedAt time.Time
}
