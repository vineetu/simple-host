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
func pricingNumbers() map[string]string {
	accountMB := config.Active().MaxAccountMB
	account := "no account-wide cap by default"
	switch {
	case accountMB == 1024:
		account = "up to 1 GB"
	case accountMB > 0:
		account = "up to " + limitNum(accountMB) + " MB"
	}
	siteTotalMB := config.Active().MaxSiteTotalMB
	siteTotal := "no per-site cap by default"
	if siteTotalMB > 0 {
		siteTotal = "up to " + limitNum(siteTotalMB) + " MB"
	}
	return map[string]string{
		"__KEEP_VERSIONS__":       limitNum(KeepVersions()),
		"__SITE_TOTAL_MB__":       siteTotal,
		"__ACCOUNT_MB__":          account,
		"__VIEWERS_MAX__":         limitNum(config.Active().SiteViewersMax),
		"__NOTIFY_MAX__":          limitNum(config.Active().NotifyMaxPerSite),
		"__STORAGE_DB_BYTES__":    limitCommas(int(storageSiteLimitBytes())),
		"__STORAGE_FILES_BYTES__": limitCommas(int(storageLimitBytes(os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"), 10000000, 10<<30))),
		"__STORAGE_FILE_BYTES__":  limitCommas(int(storageFileMaxBytes())),
		"__PHOTO_BYTES__":         limitCommas(photoLimit("PHOTO_MAX_BYTES", 1000000)),
	}
}

func applyPricingNumbers(body []byte) []byte {
	for token, value := range pricingNumbers() {
		body = bytes.ReplaceAll(body, []byte(token), []byte(value))
	}
	return body
}
