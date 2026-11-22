package hack

import (
	"github.com/vsriram/simple-host/products/hack/checklist"
	"github.com/vsriram/simple-host/products/hack/content"
	"github.com/vsriram/simple-host/products/hack/entries"
	"github.com/vsriram/simple-host/products/hack/judging"
	"github.com/vsriram/simple-host/products/hack/messages"
	"github.com/vsriram/simple-host/products/hack/notices"
	"github.com/vsriram/simple-host/products/hack/organisers"
	"github.com/vsriram/simple-host/products/hack/public"
	"github.com/vsriram/simple-host/products/hack/registration"
	"github.com/vsriram/simple-host/products/hack/results"
	"github.com/vsriram/simple-host/products/hack/teams"
	"github.com/vsriram/simple-host/products/hack/teamsites"
	"github.com/vsriram/simple-host/products/hack/voting"
	"net/http"
)

// Register adds every /v1/hack/... route: one Register per feature, each
// wrapping its routes in authMW the same way.
func (h *HackHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	checklist.Register(mux, h.Hack, authMW)
	h.assistant.Register(mux, authMW)
	public.Register(mux, h.Hack, authMW)
	content.Register(mux, h.Hack, authMW)
	voting.Register(mux, h.Hack, authMW)
	h.lifecycle.Register(mux, authMW)
	teams.Register(mux, h.Hack, authMW)
	registration.Register(mux, h.Hack, authMW)
	teamsites.Register(mux, h.Hack, authMW)
	entries.Register(mux, h.Hack, authMW)
	judging.Register(mux, h.Hack, authMW)
	results.Register(mux, h.Hack, authMW)
	organisers.Register(mux, h.Hack, authMW)
	h.exports.Register(mux, authMW)
	notices.Register(mux, h.Hack, authMW)
	messages.Register(mux, h.Hack, authMW)
}
