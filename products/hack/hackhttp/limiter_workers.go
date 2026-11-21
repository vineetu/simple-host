package hackhttp

import (
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/workers"
)

// LimiterWorkers evicts the hack limiters' idle buckets.
func (h *HackHandler) LimiterWorkers() workers.Owner {
	h.limitersOnce.Do(func() {
		for _, l := range []*httpx.RateLimiter{h.codes.ip, h.codes.user, h.namesUser, h.participation.entryWrites, h.participation.shotWrites, h.participation.applyIP, h.participation.applyUser, h.assistant.writes, h.screenshotsIP} {
			h.caches.Rate("http.hack.limiters", l, 10*time.Minute, 30*time.Minute)
		}
	})
	return &h.caches
}

// screenshotLimiter is the one public screenshot budget: the handler's when
// there is one, so the gallery page and the event host share it.
func screenshotLimiter(owners []*HackHandler) *httpx.RateLimiter {
	if len(owners) > 0 {
		return owners[0].screenshotsIP
	}
	return httpx.NewRateLimiter(120, 5)
}
