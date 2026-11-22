package hack

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"

	"github.com/vsriram/simple-host/products/hack/pages"

	"github.com/vsriram/simple-host/host/workers"
	"github.com/vsriram/simple-host/products/hack/lifecycle"

	"github.com/vsriram/simple-host/products/hack/assistant"

	"github.com/vsriram/simple-host/products/hack/exports"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// HackHandler is the hosted-events API (EVENTS=hosted): event CRUD, the startup
// setters, workers and the route table.
type HackHandler struct {
	*core.Hack

	caches       httpx.Caches
	limitersOnce sync.Once
	contentOnce  sync.Once
	contentOwner *contentDeliveryOwner
	lifecycle    *lifecycle.Handler
	exports      *exports.Handler
	// assets is the asset/chrome pipeline for RegisterDemo's pages
	// (hack_watch.go), set once by ShareAssets right after construction.
	assets pages.Pages

	assistant *assistant.Handler
}

func (h *HackHandler) ShareAssets(p pages.Pages) { h.assets = p }

// RegisterDemo serves the demo event links (pages.RegisterDemo); called only in hosted mode.
func (h *HackHandler) RegisterDemo(mux *http.ServeMux, slug string) {
	pages.RegisterDemo(mux, h.Hack, h.assets, slug)
}

func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string, editions ...*edition.Edition) *HackHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	return newHandler(core.New(database, publicBaseURL, siteDomain, ed))
}

// newHandler builds the route handlers over the module's shared state.
func newHandler(c *core.Hack) *HackHandler {
	h := &HackHandler{Hack: c, exports: exports.New(c), lifecycle: &lifecycle.Handler{Hack: c}}
	h.assistant = &assistant.Handler{Hack: c}
	return h
}

// SetAssistant wires the organiser assistant (needs host/llm and the docs text). Unset, every assistant route is off.
func (h *HackHandler) SetAssistant(a *assistant.AssistantService) { h.assistant.Service = a }

// SetNamePeer: fn reports whether the peer instance holds name; nil = no peer.
func (h *HackHandler) SetNamePeer(fn func(ctx context.Context, name string) (bool, error)) {
	h.lifecycle.NamePeer = fn
}

// SetInstanceUsage: fn returns bytes used by team sites on this instance; nil = 0.
func (h *HackHandler) SetInstanceUsage(fn func(ctx context.Context) (int64, error)) {
	h.lifecycle.InstanceUsage = fn
}

// NameTaken is what this instance answers the peer: the name is an event slug,
// a handle or a handle alias here, or reserved.
func (h *HackHandler) NameTaken(ctx context.Context, name string) (bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false, nil
	}
	if events.NameReserved(name) {
		return true, nil
	}
	v, err := h.Events.JudgeHandle(ctx, name)
	if err != nil {
		return false, err
	}
	if v.Status != 0 {
		if v.Code == "invalid_handle" {
			return false, nil
		}
		return true, nil
	}
	taken, err := h.Events.EventSlugTakenLookup(ctx, name)
	return taken, err
}

// RequestOpenEventCerts asks for the certificate of every event that is
// open, building or closed and has none yet (the back-fill; cheap: a request
// file is written only when missing).
func (h *HackHandler) RequestOpenEventCerts(ctx context.Context) {
	if h.Links.Hooks == nil {
		return
	}
	if err := h.Teams.OpenEventSlugs(ctx, func(handle string) {
		if s, ok := h.Links.Hooks.(interface{ RequestSiteCertContext(context.Context, string) }); ok {
			s.RequestSiteCertContext(ctx, handle)
		} else {
			h.Links.Hooks.RequestSiteCert(handle)
		}
	}); err != nil {
		core.SweepReport{Ctx: ctx}.Fail("list open events for certificates", err)
	}

}

// SignHackArchiveClaim seals an archive download claim (exports.Handler).
func (h *HackHandler) SignHackArchiveClaim(c exports.HackArchiveClaim) string {
	return h.exports.SignHackArchiveClaim(c)
}

// EventCleanupOwner is the daily archive cleanup worker.
func (h *HackHandler) EventCleanupOwner() workers.Owner { return h.lifecycle.EventCleanupOwner() }

// HandlerFor builds the route handlers over an existing core (the journey suites' fixture).
func HandlerFor(c *core.Hack) *HackHandler { return newHandler(c) }
