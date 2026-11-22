package hack

import (
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/workers"
)

func (h *HackHandler) LimiterWorkers() workers.Owner {
	h.limitersOnce.Do(func() {
		for _, l := range []*httpx.RateLimiter{h.Codes.IP, h.Codes.User, h.NamesUser, h.EntryWrites, h.ShotWrites, h.ApplyIP, h.ApplyUser, h.AssistantWrites, h.ScreenshotsIP} {
			h.caches.Rate("http.hack.limiters", l, 10*time.Minute, 30*time.Minute)
		}
	})
	return &h.caches
}
