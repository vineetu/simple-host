package servertest

import (
	"strconv"
)

// Cloudflare's documented test keys: this site key always passes and this
// secret always passes. Siteverify itself is a local fake here, so the
// tests never reach Cloudflare and can answer every case.
const (
	TurnstileTestSiteKey = "1x00000000000000000000AA"
	TurnstileTestSecret  = "1x0000000000000000000000000000000AA"
	TurnstileTestApp     = "https://app.simple-host.test"
)

var tokenSeq int

func (s StorageTestSite) Token(resource string) string {
	tokenSeq++
	return "ok-" + s.Host + "|" + resource + "|" + strconv.Itoa(tokenSeq)
}
