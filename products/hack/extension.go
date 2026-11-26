package hack

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/products/hack/pages"

	"github.com/vsriram/simple-host/products/hack/public"

	"github.com/vsriram/simple-host/products/hack/assistant"

	"github.com/vsriram/simple-host/products/hack/core"

	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"sync"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/llm"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/namepeer"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
	events "github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	hackdb "github.com/vsriram/simple-host/products/hack/db"
	hackmigrations "github.com/vsriram/simple-host/products/hack/db/migrations"
	hackmcp "github.com/vsriram/simple-host/products/hack/mcp"
	hackmetrics "github.com/vsriram/simple-host/products/hack/papermetrics"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// Extension describes the hosted Hack routes, workers and product policy.
func Extension() *extension.Extension {
	// The one Assets of this install: made when the platform asks for the
	// product's files (before Wire), then used by the product's own routes.
	var productAssets *pages.Assets
	var productConfig productconfig.Config
	catalog := hackdb.Catalog()
	return &extension.Extension{
		Name:            "simple-hack",
		ValidateProduct: validateInstallProduct,
		Migrations:      &catalog,
		MigrationOrder:  hackmigrations.ProductOrder,
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
		Assets: func(site *siteshttp.SiteHandler) extension.Assets {
			assets := pages.NewAssets(site.AssetServices(), site.Pages().InkBaseURL())
			productAssets = assets
			return extension.Assets{Replace: assetReplacements(), Own: ownAssetFiles(), Files: productFiles{}, Skills: hackassets.HackSkills, Hooks: assets.Hooks(), Rewrite: assets.Rewrite,
				Routes: func(mux *http.ServeMux, services web.AssetServices) {
					mux.HandleFunc("GET /skills.zip", assets.ServeHackSkillsZip())
					mux.HandleFunc("GET /skills/version", func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"version":"` + services.PluginVersion() + `"}`))
					})
					mux.Handle("GET /setup/setup.js", services.AdminUICSP(assets.ServeSetupJS()))
					RegisterOwnFiles(mux, services.ProductFiles())
					pages.RegisterHackFilm(mux)
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
			return extension.EarlyHooks{Accounts: accountapp.GroupHooks{Erasure: members.AccountErasure{}}}
		},
		Wire: func(d extension.Deps) (extension.Parts, error) {
			cfg, ed := d.Config, d.Edition
			siteHandler, mux, authMW := d.Site, d.Mux, d.AuthMW
			hack := NewHackHandler(d.DB, cfg.PublicBaseURL, cfg.SiteDomain, ed)
			hack.SetProductLimits(productConfig.Limits)
			hack.ShareAssets(siteHandler.Pages())
			hack.SetHandleChecker(func(ctx context.Context, q sqlkit.Querier, name string) (events.HandleVerdict, error) {
				v, err := accountapp.JudgeHandle(ctx, q, "", name)
				return events.HandleVerdict{Status: v.Status, Code: v.Code}, err
			})
			// sites.Upload.Body already reflects any MAX_ARCHIVE_MB override
			// (applied from host/sites' init before main runs).
			hack.SetSiteArchiveDefault(sites.Upload.Body)
			hack.SetMailer(d.Mail)
			if d.Mail != nil {
				d.Mail.SetJournal("hack_mail_send")
			}
			if cfg.EventNamePeer != "" {
				peer, zone := namepeer.NamePeerClient(cfg.EventNamePeer), cfg.SiteDomain
				hack.SetNamePeer(func(ctx context.Context, name string) (bool, error) { return peer(ctx, zone, name) })
				log.Printf("hosted events: event names checked with %s", cfg.EventNamePeer)
			}
			hack.SetInstanceUsage(siteshttp.DirUsage(cfg.DataDir))
			// Team sites: the sweep pins teams at their deadline, removes sites of removed
			// teams and asks for open events' certificates.
			hack.SetSites(siteHandler)
			d.Connector.SetTeamConnections(true)
			// Organiser assistant: off unless both are set.
			if productConfig.AssistantBaseURL != "" && productConfig.AssistantModel != "" {
				assistantLLM := llm.New(llm.Config{
					BaseURL:         productConfig.AssistantBaseURL,
					Model:           productConfig.AssistantModel,
					APIKey:          productConfig.AssistantAPIKey,
					ReasoningEffort: productConfig.AssistantReasoningEffort,
					MaxTokens:       assistant.AssistantMaxTokens,
				})
				hack.SetAssistant(assistant.NewAssistant(d.DB, assistantLLM, assistant.AssistantConfig{
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
			namepeer.RegisterNamePeer(mux, []string{cfg.SiteDomain}, func(ctx context.Context, _ string, name string) (bool, error) {
				return hack.NameTaken(ctx, name)
			})
			hackPages := public.NewHackEventPages(hackassets.Static)
			groupHosts := public.NewGroupHosts(d.DB, public.HackEventPage(d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages), public.HackScreenshot(d.DB, siteHandler.TeamSitesReady, hack.Hack))
			if productAssets == nil {
				return extension.Parts{}, fmt.Errorf("simple-hack: Assets must be installed before Wire")
			}
			pages.RegisterHackHome(mux, siteHandler.Pages(), productAssets)
			public.RegisterHackPublic(mux, d.DB, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady, hackPages, hack.Hack)
			pages.RegisterHackUI(mux, siteHandler.Pages(), siteHandler.TeamSitesFrameBase())
			return extension.Parts{
				Workers:   []workers.Owner{hack.LimiterWorkers(), hack.EventCleanupOwner(), hack.ContentDeliveryOwner()},
				SiteSweep: NewSiteSweep(d.DB, siteHandler, siteSweep),
				Hooks: extension.Hooks{
					Storage:    storagebind.Provider{DB: d.DB},
					Sitemap:    hackteams.FilterSitemapSites,
					GroupHosts: groupHosts,
					GroupGate:  teams.SiteGate{TeamSitesReady: siteHandler.TeamSitesReady, RequestCert: siteHandler.RequestSiteCert, PaperSite: siteHandler.RecordGateSite, PaperState: siteHandler.RecordGateState},
				},
				Tools: hackTools(d.DB),
				ProductMetrics: func(paper *papermetrics.Collector) {
					limits := productConfig.Limits
					limits.Limits = cfg.Limits
					hackmetrics.Install(paper)
					paper.WithRunLimits(limits)
				},
				PaperRecorder: func(next http.Handler) http.Handler {
					return core.WithPaperRecorder(papermetrics.With(next, SitePaperRecorder{}), hackmetrics.RequestRecorder{})
				},
			}, nil
		},
	}
}

// hackTools is Simple Hack's tool contribution, its usage events tagged with
// the caller's role and the event's stage from the database.
func hackTools(db *sql.DB) mcp.Contribution {
	tools := hackmcp.Contribution()
	tools.AnalyticsTags = hackmcp.AnalyticsTags(func(ctx context.Context, slug, account, user string) hackmcp.EventFact {
		var ev hackevents.Event
		var err error
		held := false
		switch {
		case slug != "":
			ev, err = hackevents.GetEventBySlug(ctx, db, slug)
		case account != "":
			ev, held, err = hackteams.HoldingAccountEvent(ctx, db, account)
			if !held {
				return hackmcp.EventFact{}
			}
		default:
			return hackmcp.EventFact{}
		}
		if err != nil {
			return hackmcp.EventFact{}
		}
		role := "none"
		if member, err := hackmembers.Member(ctx, db, ev.ID, cmp.Or(user, account), hackmembers.NoLock); err == nil {
			role = member.Role
		}
		return hackmcp.EventFact{Found: true, Role: role, Stage: ev.Stage, Listed: ev.DirectoryListed, Held: held}
	})
	return tools
}

// newAssistantDocsTextFunc returns an assistant.DocsTextFunc: host/app/hack/events
// may not import host/webassets (scripts/layering/rules.txt), so this wires
// webassets' embedded Hack skills into assistant.AssistantDocsText (the one place
// the text extraction lives) and injects the result into the assistant service.
func newAssistantDocsTextFunc() func() string {
	var once sync.Once
	var text string
	return func() string {
		once.Do(func() {
			text = assistant.AssistantDocsText(hackassets.HackSkills)
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
