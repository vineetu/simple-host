package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/mcp"
)

func TestHackPublicDiscovery(t *testing.T) {
	for _, base := range []string{"https://simple-hack.app", "https://hack.company.example", "http://localhost:18480"} {
		t.Run(base, func(t *testing.T) {
			mode, chrome, previousBase := hackMode, hackChrome, hackInkBaseURL
			SetHackMode(true)
			SetHackChrome(true)
			SetHackInstanceURL(base)
			t.Cleanup(func() { SetHackMode(mode); SetHackChrome(chrome); SetHackInstanceURL(previousBase) })
			mux := http.NewServeMux()
			RegisterUIRoutes(mux, base, chromeTestHandler())
			RegisterHackHome(mux)
			RegisterSkillsHub(mux, base)
			host := strings.SplitN(base, "://", 2)[1]
			server := mcp.NewServer(mcp.Config{APIHost: host})
			connector := &ConnectorHandler{issuer: base, mcp: server}
			connector.Register(mux, func(next http.Handler) http.Handler { return next })
			readJSON := func(path string, dst any) string {
				t.Helper()
				rec := get(t, mux, host, path)
				if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), dst) != nil {
					t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "simple-host") {
					t.Errorf("%s contains Host metadata", path)
				}
				return rec.Body.String()
			}
			var agent agentCard
			readJSON("/.well-known/agent-card.json", &agent)
			if agent.Name != "Simple Hack" || agent.Version != buildinfo.Version || agent.Endpoint != base+"/mcp" || len(agent.EndpointAliases) != 0 {
				t.Fatalf("wrong agent identity: %+v", agent)
			}
			if agent.Auth.AuthorizationServer != base+"/.well-known/oauth-authorization-server" {
				t.Fatal("wrong auth issuer")
			}
			for _, role := range []string{"Organisers", "Participants", "Judges", "selected team", "deadline"} {
				if !strings.Contains(agent.Description, role) {
					t.Errorf("missing role overview %q", role)
				}
			}
			tools := server.HackListedTools()
			if len(agent.Tools) != len(tools) || len(tools) <= 20 {
				t.Fatalf("Hack tools truncated: %d", len(agent.Tools))
			}
			seen := map[string]bool{}
			for i, tool := range agent.Tools {
				if tool.Description == "" || tool.Name != tools[i].Name || tool.Description != agentCardSummary(tools[i].Title) || seen[tool.Name] {
					t.Errorf("wrong tool %q", tool.Name)
				}
				seen[tool.Name] = true
			}
			for _, name := range []string{"hack_list_events", "hack_select_team", "update_site", "storage_list_resources"} {
				if !seen[name] {
					t.Errorf("missing Hack tool %s", name)
				}
			}
			for _, name := range []string{"find_tools", "run_tool", "hack_create_team_key", "create_import_link", "list_notifications"} {
				if seen[name] {
					t.Errorf("unexpected tool %s", name)
				}
			}
			for _, path := range []string{"/.well-known/mcp/server-card.json", "/mcp/server-card"} {
				var card mcpServerCard
				readJSON(path, &card)
				if card.Name != "app.simple-hack/mcp" || card.Title != "Simple Hack" || card.Version != buildinfo.Version || card.Icon != base+"/favicon.svg" || len(card.Remotes) != 1 || card.Remotes[0].URL != agent.Endpoint {
					t.Fatalf("wrong server card: %+v", card)
				}
			}
			var catalog aiCatalog
			readJSON("/.well-known/ai-catalog.json", &catalog)
			// Every discovery document URL must resolve anonymously on this instance.
			var walk func(any)
			walk = func(v any) {
				switch v := v.(type) {
				case map[string]any:
					for k, value := range v {
						if k == "url" || k == "href" || k == "agent_card" || k == "mcp_server_card" || k == "openapi" {
							link, ok := value.(string)
							if !ok {
								continue
							}
							if !strings.HasPrefix(link, base+"/") {
								t.Errorf("foreign discovery URL %q", link)
								continue
							}
							u, _ := url.Parse(link)
							if r := get(t, mux, host, u.Path); r.Code != 200 {
								t.Errorf("dead discovery URL %s: %d", link, r.Code)
							}
						}
						walk(value)
					}
				case []any:
					for _, value := range v {
						walk(value)
					}
				}
			}
			for _, path := range []string{"/.well-known/ard.json", "/.well-known/api-catalog", "/.well-known/ai-catalog.json"} {
				var doc any
				body := readJSON(path, &doc)
				walk(doc)
				if path == "/.well-known/ard.json" && !strings.Contains(body, "urn:air:"+host+":server:mcp") {
					t.Error("ARD lacks local Hack URN")
				}
			}
			markdown := ""
			for _, accept := range []string{"text/markdown", "text/html", "", "text/markdown;q=0, text/html", "text/html;q=0.5, text/markdown"} {
				req := httptest.NewRequest("GET", base+"/", nil)
				req.Header.Set("Accept", accept)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != 200 || rec.Header().Get("Vary") != "Accept" || rec.Header().Get("Link") != homepageLinks {
					t.Fatalf("landing: %d %v", rec.Code, rec.Header())
				}
				if acceptsMarkdown(req) {
					markdown = rec.Body.String()
					for _, want := range []string{"title: Simple Hack", "canonical: " + base + "/", "# Hackathons, now for everyone.", "[Create event](/events/new)"} {
						if !strings.Contains(markdown, want) {
							t.Errorf("markdown missing %q: %s", want, markdown)
						}
					}
					if strings.Contains(markdown, "<script") || strings.Contains(markdown, "watch the film again") {
						t.Error("film included in markdown")
					}
					copy := "Create your event page, share one link"
					if base == "https://simple-hack.app" {
						copy = "Want fresh ideas from your people?"
					}
					if !strings.Contains(markdown, copy) {
						t.Error("wrong landing selected")
					}
				} else if !strings.Contains(rec.Body.String(), `href="/index.md"`) {
					t.Error("missing HTML alternate")
				}
			}
			for _, path := range []string{"/index.md", "/llms.md"} {
				rec := get(t, mux, host, path)
				if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || rec.Body.String() != markdown {
					t.Errorf("%s differs", path)
				}
			}
			for _, query := range []string{"token=test", "cn=test"} {
				req := httptest.NewRequest("GET", base+"/?"+query, nil)
				req.Header.Set("Accept", "text/markdown")
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != 302 || rec.Header().Get("Location") != "/signin?"+query {
					t.Error("sign-in redirect changed")
				}
			}
		})
	}
}

func TestAPIErrorInstanceReference(t *testing.T) {
	for _, base := range []string{"https://simple-host.app", "https://simple-hack.app", "https://hack.company.example", "http://localhost:18480/"} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/example", func(http.ResponseWriter, *http.Request) {})
		app := APIErrors(mux, mux, base)
		for _, tc := range []struct {
			method, path string
			status       int
			code         string
		}{{"GET", "/v1/absent", 404, "not_found"}, {"POST", "/v1/example", 405, "method_not_allowed"}, {"GET", "/mcp/absent", 404, "not_found"}} {
			req := httptest.NewRequest(tc.method, "https://untrusted.example"+tc.path, nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			body := checkAPIError(t, rec, tc.status, tc.code)
			if body.Hint != "See "+strings.TrimRight(base, "/")+"/docs.html for supported routes and methods." {
				t.Errorf("wrong hint: %s", body.Hint)
			}
		}
	}
}
