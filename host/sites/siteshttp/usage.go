package siteshttp

import (
	"github.com/vsriram/simple-host/host/workers"
)

func (h *SiteHandler) NetworkUsageWorker() workers.Owner { return h.Network.NetworkUsageWorker() }
