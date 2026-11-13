package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	events "github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/llm"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/webassets"
	"github.com/vsriram/simple-host/internal/workers"
)

func hackExtension() *extension.Extension {
	return &extension.Extension{
		Name:    "simple-hack",
		Edition: func(limits config.Limits) *edition.Edition { return edition.ForKindWithLimits(edition.Hack, limits) },
		Gate:    func(d extension.GateDeps) func(http.Handler) http.Handler { return d.Site.HackLegacyStorageGate },
		Wire: func(d extension.Deps) (extension.Parts, error) {
			cfg, ed := d.Config, d.Edition
			siteHandler, mux, authMW := d.Site, d.Mux, d.AuthMW
			hack := hackhttp.NewHackHandler(d.DB, cfg.PublicBaseURL, cfg.SiteDomain, ed)
			hack.SetKeyResolver(func(ctx context.Context, key string, idle time.Duration) (model.User, error) {
				return d.Accounts.GetUserByAPIKey(ctx, key, idle)
			})
			hack.ShareAssets(siteHandler.Pages())
			hack.SetHandleChecker(func(ctx context.Context, q sqlkit.Querier, name string) (hackhttp.HandleVerdict, error) {
				v, err := handler.JudgeHandle(ctx, q, "", name)
				return hackhttp.HandleVerdict{Status: v.Status, Code: v.Code}, err
			})
			// handler.SiteLimit() already reflects any MAX_ARCHIVE_MB override
			// (applied from site.go's init before main runs), so this matches
			// the per-site default HackHandler.siteLimitFor used to read off
			// handler's shared limitSet (cleanup 3.2 slice 1c).
			hack.SetSiteArchiveDefault(handler.SiteLimit())
			hack.SetMailer(d.Mail)
			if cfg.EventNamePeer != "" {
				peer, zone := handler.NamePeerClient(cfg.EventNamePeer), cfg.SiteDomain
				hack.SetNamePeer(func(ctx context.Context, name string) (bool, error) { return peer(ctx, zone, name) })
				log.Printf("hosted events: event names checked with %s", cfg.EventNamePeer)
			}
			hack.SetInstanceUsage(handler.DirUsage(cfg.DataDir))
			// Team sites (M2): member keys act on one team site; the sweep pins
			// teams at their deadline, removes sites of removed teams and asks
			// for open events' certificates.
			hack.SetSites(siteHandler)
			d.Connector.SetHackTeams(true)
			// SIM-394 organiser assistant: off unless both are set. Built here
			// (not in hackhttp) because the client needs internal/llm and the
			// docs text needs internal/webassets, neither of which hackhttp may
			// import (scripts/layering/rules.txt).
			if cfg.AssistantBaseURL != "" && cfg.AssistantModel != "" {
				assistantLLM := llm.New(llm.Config{
					BaseURL:         cfg.AssistantBaseURL,
					Model:           cfg.AssistantModel,
					APIKey:          cfg.AssistantAPIKey,
					ReasoningEffort: cfg.AssistantReasoningEffort,
					MaxTokens:       events.AssistantMaxTokens,
				})
				hack.SetAssistant(events.NewAssistant(d.DB, assistantLLM, events.AssistantConfig{
					BaseURL:               cfg.AssistantBaseURL,
					Model:                 cfg.AssistantModel,
					APIKey:                cfg.AssistantAPIKey,
					ReasoningEffort:       cfg.AssistantReasoningEffort,
					MonthlyBudgetUSD:      cfg.AssistantMonthlyBudgetUSD,
					DailyQuestions:        cfg.AssistantDailyQuestions,
					PriceInputPerMTokUSD:  cfg.AssistantPriceInputPerMTokUSD,
					PriceOutputPerMTokUSD: cfg.AssistantPriceOutputPerMTokUSD,
				}, newAssistantDocsTextFunc()))
			}
			siteSweep := func(ctx context.Context) {
				hack.RequestOpenEventCerts(ctx)
				hack.SettleDueEntries(ctx)
			}
			hack.Register(mux, authMW)
			hack.RegisterDemo(mux, os.Getenv("HACK_DEMO_EVENT_SLUG"))
			handler.RegisterNamePeer(mux, []string{cfg.SiteDomain}, func(ctx context.Context, _ string, name string) (bool, error) {
				return hack.NameTaken(ctx, name)
			})
			hackPages := hackhttp.NewHackEventPages(handler.EmbeddedStatic())
			siteHandler.SetHackEventPage(hackhttp.HackEventPage(d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages))
			siteHandler.SetHackScreenshot(hackhttp.HackScreenshot(d.DB, siteHandler.TeamSitesReady, hack))
			hackhttp.RegisterHackHome(mux, siteHandler.Pages())
			hackhttp.RegisterHackPublic(mux, d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages)
			hackhttp.RegisterHackUI(mux, siteHandler.Pages(), siteHandler.TeamSitesFrameBase())
			if d.ImageScan != nil {
				hack.SetImageScan(d.ImageScan)
				// SetImageScan takes only the enqueue interface; wire the reverse edge here.
				d.ImageScan.SetHackTakeDownSync(hack.SyncEventMarkers)
			}
			return extension.Parts{
				Workers:   []workers.Owner{hack.LimiterWorkers(), hack.EventCleanupOwner(), hack.ContentDeliveryOwner()},
				SiteSweep: siteSweep,
				PaperHack: func(paper *papermetrics.Collector) { paper.WithHack() },
				PaperRecorder: func(next http.Handler) http.Handler {
					return hackhttp.WithPaperRecorder(next, papermetrics.RequestRecorder{})
				},
			}, nil
		},
	}
}

// newAssistantDocsTextFunc returns a events.DocsTextFunc: internal/app/hack/events
// may not import internal/webassets (scripts/layering/rules.txt), so this
// wires webassets' embedded docs tree into hackhttp.AssistantDocsText (the
// one place the HTML-to-text extraction lives -- SIM-394 L22) and injects
// the result into the assistant service. The extraction (parse + strip +
// collapse whitespace for four small pages) runs once, the first time any
// request calls the returned function, and the result is cached for the
// life of the process.
func newAssistantDocsTextFunc() func() string {
	var once sync.Once
	var text string
	return func() string {
		once.Do(func() {
			text = hackhttp.AssistantDocsText(webassets.DocsTree(true))
		})
		return text
	}
}
