package handler

import (
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/workers"
)

// VisitorAuthWorkers returns the account service's sign-in expiry owner.
func (h *OAuthHandler) VisitorAuthWorkers() workers.Owner {
	h.visitorWorkerOnce.Do(func() { h.visitorWorker = accounts.NewVisitorAuthWorker(h.accounts) })
	return h.visitorWorker
}
