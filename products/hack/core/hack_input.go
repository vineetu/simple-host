package core

import (
	"database/sql"
	"time"
)

func RFC3339(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func RFC3339Time(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
