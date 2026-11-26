# check-docs-sync.sh needs python3 with PyYAML; check-fresh-install.sh needs a
# local postgres superuser (it creates and drops a throwaway database).
# check-prices-age.sh only warns: the cost calculator's prices are checked monthly.
# check-release needs network access, Docker and nginx; it holds publication,
# local settings and HTTP/server fixture checks.
#
# `test` is the fast loop: go test ./... with no database, so the ~15 DB-backed
# fixtures (see scripts/test-integration.sh) skip. `test-integration` gives
# them a disposable Postgres (TEST_PG_URL, or Docker if that's unset) and
# fails if any of them still skip -- that's what `check` runs. Platform packages
# use the platform schema alone; products/ packages use the composed schema.
# Packages whose inputs did not change answer from Go's test cache; FULL=1
# (`make check FULL=1`, what installs and CI run) runs every package.
# `test-db` is the same run narrowed for chasing a flaky test, e.g.
#   make test-db PKGS=./host/server/servertest/datatest RUN='^TestNotify' COUNT=5
.PHONY: ready check check-release connector-check inventory test test-rules test-integration feature test-db boundary-report check-boundaries browser

# `check` stays the default goal (`make` with no target) even though `test`
# and `test-integration` are declared above it.
.DEFAULT_GOAL := check

# DB_DSN and MIGRATE_TEST_DSN are cleared explicitly: one left set in the
# calling shell would silently turn this back into a DB-backed run.
test:
	DB_DSN= MIGRATE_TEST_DSN= go test ./...
	$(MAKE) test-rules

test-rules:
	cd host/storage/rules && go vet ./... && go test ./...

test-integration:
	bash scripts/test-integration.sh $(if $(FULL),--full)

# The loop while changing one feature: its tests (cached) without the whole-server
# journeys, which `make ready` and --full still run before an install.
feature:
	bash scripts/test-integration.sh --feature

PKGS ?= ./...
COUNT ?= 1
RUN ?=
test-db:
	TEST_PKGS='$(PKGS)' TEST_COUNT='$(COUNT)' bash scripts/test-integration.sh $(if $(RUN),-run '$(RUN)')

check:
	@test -z "$$(gofmt -l host/ cmd/ $$(if [ -d products/ ]; then echo products/; fi))" || { echo "gofmt: $$(gofmt -l host/ cmd/ $$(if [ -d products/ ]; then echo products/; fi))"; exit 1; }
	go build ./...
	cd host/storage/rules && go build ./...
	go vet ./...
	$(MAKE) test-rules
	bash scripts/test-integration.sh $(if $(FULL),--full)
	node --test scripts/tests/arrival.test.cjs scripts/tests/photo-process.test.cjs scripts/tests/my-sites.test.cjs scripts/tests/site-preview.test.cjs scripts/tests/hack-stage-announce.test.mjs scripts/tests/hack-publish-results.test.mjs scripts/tests/hack-event-times.test.mjs scripts/tests/hack-navigation.test.mjs
	bash scripts/check-html.sh
	bash scripts/check-layering.sh
	bash scripts/split/namescan.sh
	bash scripts/check-public-text.sh
	bash scripts/check-binary.sh
	go run ./scripts/split/sqlcheck -tests -report -
	go run ./scripts/split/sqlcheck -migrations
	go run ./scripts/split/sqlcheck -owners
	go run ./scripts/inventory check
	bash scripts/check-docs-sync.sh
	bash deploy/cert-issuers/runtime_test.sh
	python3 deploy/cert-issuers/requeue_test.py
	bash deploy/cert-issuers/watch_test.sh
	bash deploy/cert-issuers/fallback_test.sh
	python3 deploy/cert-issuers/google_pacing_test.py
	bash deploy/domain-certs/issue_test.sh
	bash deploy/family-certs/issue_test.sh
	bash deploy/prod/family-adopt_test.sh
	bash deploy/site-certs/site-certs_test.sh
	bash deploy/prod/nginx-suspended-marker_test.sh
	bash deploy/prod/nginx-analytics-logformat-apply_test.sh
	bash deploy/prod/sh-network-watch_test.sh
	bash deploy/prod/sh-uptime-watch_test.sh
	bash deploy/install/install_test.sh
	bash deploy/hack/standalone/tests/install_test.sh
	bash deploy/shared/render_test.sh
	bash deploy/digitalocean/droplet/test/pins_test.sh
	bash scripts/check-fresh-install.sh
	@bash scripts/check-prices-age.sh || echo "WARNING: the cost calculator's prices need their monthly check (not a failure)"

# `ready` is what to run before every commit. It formats the Go code,
# regenerates the generated files (make inventory) and runs the checks a
# code change can break, with the cached DB-backed test run. `check` (CI)
# runs everything.
ready:
	gofmt -w host/ cmd/ products/ scripts/
	go build ./...
	go vet ./...
	$(MAKE) test-rules
	$(MAKE) inventory
	go run ./scripts/inventory check
	go run ./scripts/split/sqlcheck -migrations
	go run ./scripts/split/sqlcheck -owners
	bash scripts/check-docs-sync.sh
	bash scripts/check-layering.sh
	bash scripts/test-integration.sh
	@echo "ready: commit the changes, generated files included (git status)"

# The MCP connector check of one product (plugins/ROUTINE.md):
#   make connector-check PRODUCT=host|hack [NO_EVALS=1]
connector-check:
	PRODUCT=$(or $(PRODUCT),host) bash scripts/mcpeval/check.sh

# Publication, cross-repository and HTTP/server fixtures need network access.
check-release:
	bash deploy/kubernetes/test-chart.sh
	bash scripts/check-release.sh

# The browser checks, one after another; every one runs even after a failure.
browser:
	@fail=0; for name in hack-navigation hack-checklists hack-forms storage-rules named-viewers signin-card owner-dashboard; do \
		bash scripts/split/browser-check.sh $$name || fail=1; \
	done; exit $$fail

inventory:
	python3 scripts/compose-openapi.py
	python3 scripts/build-features-page.py
	go run ./scripts/inventory write

# The same five enforced boundaries run before either product is released.
# Public text uses GOLDEN_TEXT_DIR when supplied, otherwise disposable TEST_PG_URL.
check-boundaries:
	bash scripts/check-layering.sh
	bash scripts/split/namescan.sh
	go run ./scripts/split/sqlcheck -tests -report -
	go run ./scripts/split/sqlcheck -owners
	go test ./host/webassets -run '^TestPlatformEmbeddedText$$' -count=1
	bash scripts/check-public-text.sh
	bash scripts/check-binary.sh

# Retain the existing developer command, now enforcing its findings.
boundary-report:
	bash scripts/split/namescan.sh
	bash scripts/check-public-text.sh

# Simple Hack's new web app (web/hack, on the kit in web/kit). The Go build
# needs none of this: it embeds the committed build in products/hack/web/dist/files.
HACK_WEB := web
HACK_NPM := cd $(HACK_WEB) && npm
HACK_STALE := $(HACK_WEB)/hack/.stale-check
HACK_DIST := products/hack/web/dist/files

.PHONY: hack-web hack-web-deps hack-web-pages hack-web-stale hack-web-check hack-web-accept hack-web-real

# The pages the Go server writes (event page, marketing, status), rendered
# with every file they load into web/hack/.catalogue-pages for the catalogue.
HACK_PAGES := $(abspath $(HACK_WEB)/hack/.catalogue-pages)
hack-web-pages:
	rm -rf $(HACK_PAGES)
	SH_HACK_PAGES=$(HACK_PAGES) go test ./products/hack/public -run TestWriteEventPages -count=1
	SH_HACK_PAGES=$(HACK_PAGES) go test ./products/hack -run TestWriteCataloguePages -count=1

hack-web-deps:
	@[ -d $(HACK_WEB)/node_modules ] || ($(HACK_NPM) ci --no-audit --no-fund)

# Rebuild the app into products/hack/web/dist/files (commit the result).
hack-web: hack-web-deps
	$(HACK_NPM) run hack:build

# Fails when products/hack/web/dist/files is not what the source builds to.
hack-web-stale: hack-web-deps
	cd $(HACK_WEB) && SH_WEB_OUT=.stale-check npx vite build -c hack/vite.config.ts --logLevel error
	@diff -r $(HACK_STALE) $(HACK_DIST) >/dev/null || (echo "$(HACK_DIST) is stale: run make hack-web and commit it"; exit 1)
	@echo "$(HACK_DIST) is up to date"

# Everything: types, lint, unit tests (with the navigation map in step), the
# stale check, and every catalogue entry at 390 and 1280 against its baseline (with axe).
hack-web-check: hack-web-deps hack-web-pages
	python3 scripts/build-hack-navigation.py --check
	$(HACK_NPM) run hack:typecheck
	$(HACK_NPM) run lint
	$(HACK_NPM) run hack:test
	$(MAKE) hack-web-stale
	$(HACK_NPM) run hack:catalogue
	$(HACK_NPM) run hack:visual

# Accept new baseline pictures after a deliberate change (look at them first).
hack-web-accept: hack-web-deps hack-web-pages
	$(HACK_NPM) run hack:catalogue
	$(HACK_NPM) run hack:visual:accept

# The real simple-hack binary on a throwaway database (in the test Postgres
# the web-real check uses, 127.0.0.1:54329), signed in and clicked through the
# rebuilt screens at both widths. Pictures land in web/hack/test-results/real.
hack-web-real: hack-web-deps
	$(HACK_NPM) run hack:real
