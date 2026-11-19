package hack

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"

	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"sync"
	"time"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/llm"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
	"github.com/vsriram/simple-host/host/workers"
	events "github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	hackdb "github.com/vsriram/simple-host/products/hack/db"
	hackmigrations "github.com/vsriram/simple-host/products/hack/db/migrations"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
	hackmcp "github.com/vsriram/simple-host/products/hack/mcp"
	hackmetrics "github.com/vsriram/simple-host/products/hack/papermetrics"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// Extension describes the hosted Hack routes, workers and product policy.
func Extension() *extension.Extension {
	// The one Assets of this install: made when the platform asks for the
	// product's files (before Wire), then used by the product's own routes.
	var productAssets *hackhttp.Assets
	var productConfig productconfig.Config
	catalog := hackdb.Catalog()
	return &extension.Extension{
		Name:                    "simple-hack",
		LoadRoutingFacts:        hackevents.LoadRoutingFacts,
		LookupGroupRoutingFacts: hackevents.LookupRoutingFacts,
		ValidateProduct:         validateInstallProduct,
		Migrations:              &catalog,
		MigrationOrder:          hackmigrations.ProductOrder,
		Config: extension.ConfigSpec{Settings: productconfig.Settings(), Load: func(getenv func(string) string) ([]string, error) {
			var warnings []string
			var err error
			productConfig, warnings, err = productconfig.Load(getenv)
			if err == nil {
				if changed := productConfig.Limits.Changed(); len(changed) > 0 {
					log.Printf("limits changed from the defaults: %s", strings.Join(changed, " "))
				}
			}
			return warnings, err
		}},
		Assets: func(site *handler.SiteHandler) extension.Assets {
			assets := hackhttp.NewAssets(site.AssetServices(), site.Pages().InkBaseURL())
			productAssets = assets
			return extension.Assets{Replace: assetReplacements(), Own: ownAssetFiles(), Files: hackassets.Static, Skills: hackassets.HackSkills, Hooks: assets.Hooks(), Rewrite: assets.Rewrite,
				Routes: func(mux *http.ServeMux, services handler.AssetServices) {
					mux.HandleFunc("GET /skills.zip", assets.ServeHackSkillsZip())
					mux.HandleFunc("GET /skills/version", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"version":"` + services.PluginVersion() + `"}`))
					})
					mux.Handle("GET /setup/setup.js", services.AdminUICSP(assets.ServeSetupJS()))
					RegisterOwnFiles(mux, services.ProductFiles())
					hackhttp.RegisterHackFilm(mux)
				}}
		},
		CheckSchema: func(ctx context.Context, database *sql.DB) error {
			if err := hackdb.VerifyHackSchema(ctx, database); err != nil {
				return fmt.Errorf("schema check (EVENTS=hosted): %w", err)
			}
			return nil
		},
		Edition: func(limits config.Limits) *edition.Edition {
			return productedition.ForKindWithLimits(productedition.Hack, limits)
		},
		Early: func(d extension.EarlyDeps) extension.EarlyHooks {
			return extension.EarlyHooks{Accounts: accountapp.GroupHooks{Erasure: members.AccountErasure{}, Keys: hackevents.GroupKeys{}, Target: hackteams.ConnectorTarget{}}}
		},
		Wire: func(d extension.Deps) (extension.Parts, error) {
			cfg, ed := d.Config, d.Edition
			siteHandler, mux, authMW := d.Site, d.Mux, d.AuthMW
			hack := hackhttp.NewHackHandler(d.DB, cfg.PublicBaseURL, cfg.SiteDomain, ed)
			hack.SetProductLimits(productConfig.Limits)
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
			if d.Mail != nil {
				d.Mail.SetJournal("hack_mail_send")
			}
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
			d.Connector.SetTeamConnections(true)
			// SIM-394 organiser assistant: off unless both are set. Built here
			// (not in hackhttp) because the client needs host/llm and the
			// docs text needs host/webassets, neither of which hackhttp may
			// import (scripts/layering/rules.txt).
			if productConfig.AssistantBaseURL != "" && productConfig.AssistantModel != "" {
				assistantLLM := llm.New(llm.Config{
					BaseURL:         productConfig.AssistantBaseURL,
					Model:           productConfig.AssistantModel,
					APIKey:          productConfig.AssistantAPIKey,
					ReasoningEffort: productConfig.AssistantReasoningEffort,
					MaxTokens:       events.AssistantMaxTokens,
				})
				hack.SetAssistant(events.NewAssistant(d.DB, assistantLLM, events.AssistantConfig{
					BaseURL:               productConfig.AssistantBaseURL,
					Model:                 productConfig.AssistantModel,
					APIKey:                productConfig.AssistantAPIKey,
					ReasoningEffort:       productConfig.AssistantReasoningEffort,
					MonthlyBudgetUSD:      productConfig.AssistantMonthlyBudgetUSD,
					DailyQuestions:        productConfig.AssistantDailyQuestions,
					PriceInputPerMTokUSD:  productConfig.AssistantPriceInputPerMTokUSD,
					PriceOutputPerMTokUSD: productConfig.AssistantPriceOutputPerMTokUSD,
				}, newAssistantDocsTextFunc()))
			}
			siteSweep := func(ctx context.Context) {
				hack.RequestOpenEventCerts(ctx)
				hack.SettleDueEntries(ctx)
			}
			hack.Register(mux, authMW)
			hack.RegisterDemo(mux, productConfig.DemoEventSlug)
			handler.RegisterNamePeer(mux, []string{cfg.SiteDomain}, func(ctx context.Context, _ string, name string) (bool, error) {
				return hack.NameTaken(ctx, name)
			})
			hackPages := hackhttp.NewHackEventPages(hackassets.Static)
			groupHosts := hackhttp.NewGroupHosts(d.DB, hackhttp.HackEventPage(d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages), hackhttp.HackScreenshot(d.DB, siteHandler.TeamSitesReady, hack))
			if productAssets == nil {
				return extension.Parts{}, fmt.Errorf("simple-hack: Assets must be installed before Wire")
			}
			hackhttp.RegisterHackHome(mux, siteHandler.Pages(), productAssets)
			hackhttp.RegisterHackPublic(mux, d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages)
			hackhttp.RegisterHackUI(mux, siteHandler.Pages(), siteHandler.TeamSitesFrameBase())
			return extension.Parts{
				Workers:   []workers.Owner{hack.LimiterWorkers(), hack.EventCleanupOwner(), hack.ContentDeliveryOwner()},
				SiteSweep: NewSiteSweep(d.DB, siteHandler, siteSweep),
				Hooks: extension.Hooks{
					Storage:       storagebind.Provider{DB: d.DB},
					Sitemap:       hackevents.FilterSitemapSites,
					Notifications: NotificationGroups{},
					GroupHosts:    groupHosts,
					GroupGate:     teams.SiteGate{TeamSitesReady: siteHandler.TeamSitesReady, RequestCert: siteHandler.RequestSiteCert, PaperSite: siteHandler.RecordGateSite, PaperState: siteHandler.RecordGateState},
				},
				Tools: hackmcp.Contribution(),
				ProductMetrics: func(paper *papermetrics.Collector) {
					limits := productConfig.Limits
					limits.Limits = cfg.Limits
					hackmetrics.Install(paper)
					paper.WithRunLimits(limits)
				},
				PaperRecorder: func(next http.Handler) http.Handler {
					return hackhttp.WithPaperRecorder(handler.WithPaperRecorder(next, hackhttp.SitePaperRecorder{}), hackmetrics.RequestRecorder{})
				},
			}, nil
		},
	}
}

// newAssistantDocsTextFunc returns a events.DocsTextFunc: host/app/hack/events
// may not import host/webassets (scripts/layering/rules.txt), so this
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
			text = hackhttp.AssistantDocsText(func() fs.FS { tree, _ := fs.Sub(hackassets.Static, "docs-site/hack"); return tree }())
		})
		return text
	}
}

// validateInstallProduct is read-only evidence for adopting an existing install.
func validateInstallProduct(ctx context.Context, conn *sql.Conn) error {
	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.events') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("this database has no events table; cannot record simple-hack")
	}
	return nil
}
