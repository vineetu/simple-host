package teams

import "database/sql"

// Service owns Hack team workflows.
type Service struct{ db *sql.DB }

// New constructs a team service.
func New(database *sql.DB) *Service { return &Service{db: database} }

const teamCodeLength = 8
