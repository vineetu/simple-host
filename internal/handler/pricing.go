package handler

import (
	"bytes"
	"os"

	"github.com/vsriram/simple-host/internal/config"
)

// pricingNumbers answers the handful of /pricing figures that are not
// already covered by limitstext.go's phrase rewriter, because the knob is
// read directly from the environment (storage and photo byte caps) or lives
// outside config.Limits (the version-retention package var). Computed fresh
// on every request, straight from this install's own settings, the same
// source every other page or email draws its numbers from.
func pricingNumbers(hostedEvents bool, l config.Limits) map[string]string {
	accountMB := l.MaxAccountMB
	account := "no account-wide cap by default"
	switch {
	case accountMB == 1024:
		account = "up to 1 GB"
	case accountMB > 0:
		account = "up to " + limitNum(accountMB) + " MB"
	}
	siteTotalMB := l.MaxSiteTotalMB
	siteTotal := "no per-site cap by default"
	if siteTotalMB > 0 {
		siteTotal = "up to " + limitNum(siteTotalMB) + " MB"
	}
	// Files bucket per site: the same branch as (*SiteHandler).storageFilesLimitBytes,
	// which falls back to the KV+SQLite pool in Hack mode rather than its own env var.
	filesBytes := storageSiteLimitBytes(hostedEvents)
	if !hostedEvents {
		filesBytes = storageLimitBytes(os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"), 10000000, 10<<30)
	}
	return map[string]string{
		"__KEEP_VERSIONS__":       limitNum(KeepVersions()),
		"__SITE_TOTAL_MB__":       siteTotal,
		"__ACCOUNT_MB__":          account,
		"__VIEWERS_MAX__":         limitNum(l.SiteViewersMax),
		"__NOTIFY_MAX__":          limitNum(l.NotifyMaxPerSite),
		"__STORAGE_DB_BYTES__":    limitCommas(int(storageSiteLimitBytes(hostedEvents))),
		"__STORAGE_FILES_BYTES__": limitCommas(int(filesBytes)),
		"__STORAGE_FILE_BYTES__":  limitCommas(int(storageFileMaxBytes())),
		"__PHOTO_BYTES__":         limitCommas(photoLimit("PHOTO_MAX_BYTES", 1000000)),
	}
}

func applyPricingNumbers(body []byte, hostedEvents bool, l config.Limits) []byte {
	for token, value := range pricingNumbers(hostedEvents, l) {
		body = bytes.ReplaceAll(body, []byte(token), []byte(value))
	}
	return body
}
