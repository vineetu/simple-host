package db

import (
	"github.com/vsriram/simple-host/internal/repository/hackentries"
)

var (
	// GetEventScreenshot is used by internal/handler.
	GetEventScreenshot = hackentries.GetEventScreenshot
)
