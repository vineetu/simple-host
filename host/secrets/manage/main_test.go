package manage

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/secrets"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/host/web"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// ownDomain is this test install's own domain.
const ownDomain = "hostedhere.com"

// app is the secret routes alone, behind Access's key middleware and scope gate.
type app struct {
	*rt.Server
	db      *sql.DB
	store   *secrets.Store
	keyFile string
	logs    *bytes.Buffer
}

func newApp(t *testing.T) *app {
	t.Helper()
	database := rt.DB(t)
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountsrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "secrets.key")
	store := secrets.New(database, keyFile)
	// A new master file is made only while no secret is saved anywhere.
	t.Cleanup(func() { database.Exec(`DELETE FROM site_secrets`) })
	mux := http.NewServeMux()
	// The install's own names, as a public install has them (rt.SiteDomain is
	// a .test name, which the destination rule refuses anyway).
	hosts := sites.NewHosts(database, rt.Disk(t), edition.Default(config.DefaultLimits()), ownDomain, "sites."+ownDomain, "cname."+ownDomain, "")
	hosts.SetPlatformZones("events-" + ownDomain)
	Register(mux, database, hosts, store, auth.Middleware(adminKey, adminID, accountapp.New(database), 0), web.NoticeMiddleware("1.0.0"))
	// Everything the program logs during the test, to prove no value reaches it.
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &app{Server: rt.NewServer(t, auth.ScopeGate(accountapp.New(database), mux, 0, nil)), db: database, store: store, keyFile: keyFile, logs: logs}
}

func (a *app) call(t *testing.T, p rt.Person, method, path string, body any) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, path, body, p.Header())
}

// put saves a secret on p's site.
func (a *app) put(t *testing.T, p rt.Person, site, name, value, destination, who string) rt.Resp {
	t.Helper()
	return a.call(t, p, "PUT", "/v1/sites/"+site+"/secrets/"+name, map[string]string{"value": value, "destination": destination, "who": who, "attach": "header", "attach_name": "Authorization", "attach_prefix": "Bearer "})
}

// noValue fails when the value, or a long piece of it, is in b.
func noValue(t *testing.T, what string, b []byte, value string) {
	t.Helper()
	if bytes.Contains(b, []byte(value)) || bytes.Contains(b, []byte(value[:12])) || bytes.Contains(b, []byte(value[len(value)-12:])) {
		t.Fatalf("%s shows the value: %s", what, b)
	}
}

func deploy(t *testing.T, a *app, p rt.Person, site string) string {
	t.Helper()
	return rt.Deploy(t, a.db, rt.Disk(t), p, site, map[string]string{"index.html": "<h1>" + site + "</h1>"})
}
