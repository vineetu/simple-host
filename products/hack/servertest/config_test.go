package servertest

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/papermetrics"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
)

func TestProductLimitsProvenanceAndStartupReport(t *testing.T) {
	for _, knob := range productconfig.Knobs() {
		t.Setenv(knob.Env, "")
	}
	var baseline string
	for _, name := range []string{"defaults", "team size", "rate", "platform limit", "host mode", "equivalent rate", "assistant secret"} {
		t.Run(name, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			cfg.PersonHosts = "off"
			var changed string
			switch name {
			case "team size":
				t.Setenv("EVENT_TEAM_SIZE_DEFAULT", "12")
				changed = "EVENT_TEAM_SIZE_DEFAULT=12"
			case "rate":
				t.Setenv("RATE_LIMIT_EVENT_CODES_USER", "10,6s")
				changed = "RATE_LIMIT_EVENT_CODES_USER=10,6s"
			case "platform limit":
				cfg.Limits.MaxSitesPerAccount++
			case "host mode":
				cfg.PersonHosts = "canonical"
			case "equivalent rate":
				t.Setenv("RATE_LIMIT_EVENT_CODES_USER", "20,3000ms")
			case "assistant secret":
				t.Setenv("ASSISTANT_API_KEY", "test-only-not-provenance")
			}
			var paper *papermetrics.Collector
			ext := hack.Extension()
			wire := ext.Wire
			ext.Wire = func(d extension.Deps) (extension.Parts, error) {
				parts, err := wire(d)
				install := parts.ProductMetrics
				parts.ProductMetrics = func(c *papermetrics.Collector) {
					install(c)
					paper = c
				}
				return parts, err
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			platformtest.Build(t, cfg, []*extension.Extension{ext})
			if changed != "" && !strings.Contains(output.String(), "limits changed from the defaults: "+changed) {
				t.Fatalf("startup did not report %s", changed)
			}
			if changed == "" && strings.Contains(output.String(), "EVENT_TEAM_SIZE_DEFAULT=") {
				t.Fatal("startup reported a default product limit as changed")
			}
			if paper == nil {
				t.Fatal("product metrics hook was not called")
			}
			run, err := paper.EnsureRun(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var hash string
			if err := paper.DB().QueryRow(`SELECT config_hash FROM paper_run WHERE run_id=$1`, run).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if name == "defaults" {
				baseline = hash
			}
			wantSame := name == "defaults" || name == "equivalent rate" || name == "assistant secret"
			if (hash == baseline) != wantSame {
				t.Fatalf("config hash equals defaults = %t, want %t", hash == baseline, wantSame)
			}
		})
	}
}
