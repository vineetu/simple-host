package accesshttp

import (
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/workers"
)

// VisitorAuthWorkers returns the account service's sign-in expiry owner.
func (h *OAuthHandler) VisitorAuthWorkers() workers.Owner {
	h.visitorWorkerOnce.Do(func() { h.visitorWorker = accounts.NewVisitorAuthWorker(h.Accounts) })
	return h.visitorWorker
}
