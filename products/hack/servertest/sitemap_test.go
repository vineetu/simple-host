package servertest

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	siteapp "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/extension"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

// SIM-441's Hack fixture lives with the product so the platform's tests do
// not import Hack to install its sitemap filter. Each case exercises Build.
func TestHostedHackSitemapOnlyTeamSites(t *testing.T) {
	for _, tc := range []struct {
		name, domain string
		takenDown    bool
	}{
		{"hosted", "simple-hack.app", false},
		{"organiser take-down", "simple-hack.app", true},
		{"company install", "company.example.org", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			cfg.SiteDomain, cfg.ContentHost = tc.domain, "sites."+tc.domain
			cfg.PublicBaseURL = "https://" + tc.domain
			cfg.PersonHosts, cfg.SiteHosts = "canonical", "canonical"
			t.Setenv("PAPER_METRICS", "off")
			var db *sql.DB
			var disk *storage.DiskStorage
			var filter siteapp.SitemapFilter
			ext := hack.Extension()
			wire := ext.Wire
			ext.Wire = func(d extension.Deps) (extension.Parts, error) {
				db, disk = d.DB, d.Disk
				parts, err := wire(d)
				filter = parts.Hooks.Sitemap
				return parts, err
			}
			app := platformtest.Build(t, cfg, []*extension.Extension{ext})
			if filter == nil {
				t.Fatal("Hack extension omitted sitemap filter")
			}
			var uid, eventID string
			if err := db.QueryRow(`INSERT INTO users (username,handle,event_account) VALUES ('sitemap@example.com','sitemap-event',true) RETURNING id`).Scan(&uid); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`INSERT INTO events (slug,account_id,title,organiser_name,contact_email,join_code,judge_code)
 VALUES ('sitemap-event',$1,'Sitemap','Test','test@example.com','sitemap-join','sitemap-judge') RETURNING id`, uid).Scan(&eventID); err != nil {
				t.Fatal(err)
			}
			var input []repo.Site
			for _, name := range []string{"team", "event-page", "orphan", "second-team"} {
				var site repo.Site
				if err := db.QueryRow(`INSERT INTO sites (user_id,name) VALUES ($1,$2) RETURNING id`, uid, name).Scan(&site.ID); err != nil {
					t.Fatal(err)
				}
				site.Name, site.UserID = name, uid
				site.OwnerHandle, site.SiteURL = "sitemap-event", "https://"+name+".sitemap-event."+tc.domain+"/"
				input = append(input, site)
				dir := filepath.Join(disk.SiteDir(uid, name), "current")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Open site</h1>"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`INSERT INTO event_teams (event_id,slug,name,code) VALUES ($1,'team','Team','sitemap-team'), ($1,'second-team','Second','sitemap-second')`, eventID); err != nil {
				t.Fatal(err)
			}
			if tc.takenDown {
				// The site row stays open: a team take-down while the site was
				// in trash must still exclude it after restore.
				if _, err := db.Exec(`UPDATE event_teams SET site_taken_down_at=now() WHERE event_id=$1 AND slug='team'`, eventID); err != nil {
					t.Fatal(err)
				}
			}
			// Direct application calls use the same filter that Build installs.
			svc := siteapp.New(db, disk, nil, 0, siteapp.Deps{Sitemap: filter})
			got, err := svc.ListSitemapSites(t.Context())
			wantCount := 2
			if tc.takenDown {
				wantCount = 1
			}
			if err != nil || len(got) != wantCount {
				t.Fatalf("application sitemap: %d sites, %v", len(got), err)
			}
			for i := 1; i < len(got); i++ {
				if got[i-1].ID >= got[i].ID {
					t.Fatal("application sitemap lost repository order")
				}
			}
			// Filter order follows its input, even when it is not ID order;
			// the returned records retain all metadata supplied by the caller.
			input = []repo.Site{input[3], input[2], input[0], input[1]}
			want := []repo.Site{input[0]}
			if !tc.takenDown {
				want = append(want, input[2])
			}
			got, err = filter(t.Context(), db, input)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("filter order/metadata: got %+v, %v; want %+v", got, err, want)
			}
			w := httptest.NewRecorder()
			app.Handler.ServeHTTP(w, httptest.NewRequest("GET", cfg.PublicBaseURL+"/sitemap.xml", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("sitemap: %d %s", w.Code, w.Body)
			}
			for _, name := range []string{"team", "event-page", "orphan", "second-team"} {
				included := strings.Contains(w.Body.String(), "<loc>https://"+name+".sitemap-event."+tc.domain+"/</loc>")
				want := tc.domain == "simple-hack.app" && (name == "second-team" || name == "team" && !tc.takenDown)
				if included != want {
					t.Errorf("%s included=%v, want %v", name, included, want)
				}
			}
		})
	}
}
