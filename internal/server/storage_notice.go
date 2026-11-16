package server

import (
	"context"
	"database/sql"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	"log"
)

// logStoragePresetNotice runs once during startup, from the recipient query.
func logStoragePresetNotice(ctx context.Context, db *sql.DB) error {
	n, err := repo.PresetNoticeCount(ctx, db)
	if err == nil {
		log.Printf("%d SQLite and key-value resources still use presets. Visitor access to them stops when STORAGE_SWITCH_AT passes. See the storage rules guide.", n)
	}
	return err
}
