package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

func LegacyDeletedCursor(v string) model.LegacyDeletedCursor {
	return model.ParseLegacyDeletedCursor(v)
}
func LegacyDeletedNext(it model.LegacyDeletedItem) string { return model.LegacyDeletedNext(it) }
