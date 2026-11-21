package web

import (
	"os"

	appstorage "github.com/vsriram/simple-host/host/data"
)

func storageLimitBytes(raw string, fallback, max int64) int64 {
	return appstorage.LimitBytes(raw, fallback, max)
}
func storageSiteLimitBytes(combined bool) int64 {
	return appstorage.SiteLimitBytes(os.Getenv("SITE_STORAGE_MAX_BYTES"), combined)
}
