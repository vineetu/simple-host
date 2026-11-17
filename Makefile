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
# `test-db` is the same run narrowed for chasing a flaky test, e.g.
#   make test-db PKGS=./host/handler RUN='^TestNotify' COUNT=5
.PHONY: check check-release inventory test test-rules test-integration test-db boundary-report check-boundaries parity

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
	bash scripts/test-integration.sh

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
	bash scripts/test-integration.sh
	node --test scripts/tests/arrival.test.cjs scripts/tests/photo-process.test.cjs scripts/tests/my-sites.test.cjs scripts/tests/site-preview.test.cjs scripts/tests/hack-stage-announce.test.mjs scripts/tests/hack-publish-results.test.mjs scripts/tests/hack-event-times.test.mjs scripts/tests/hack-navigation.test.mjs scripts/golden/tests/*.test.mjs
	bash scripts/check-html.sh
	bash scripts/check-layering.sh
	bash scripts/split/namescan.sh
	bash scripts/check-public-text.sh
	bash scripts/check-binary.sh
	bash scripts/split/check-all_test.sh
	bash scripts/split/move_test.sh
	go run ./scripts/split/sqlcheck -tests -report -
	go run ./scripts/split/sqlcheck -migrations
	bash scripts/check-docs-sync.sh
	bash scripts/check-claude-plugin.sh
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
	bash scripts/check-parity-outcomes.sh
	bash scripts/parity/test.sh
	bash deploy/digitalocean/droplet/test/pins_test.sh
	bash scripts/check-fresh-install.sh
	@bash scripts/check-prices-age.sh || echo "WARNING: the cost calculator's prices need their monthly check (not a failure)"

# Publication, cross-repository and HTTP/server fixtures need network access.
check-release:
	bash deploy/kubernetes/test-chart.sh
	bash scripts/check-release.sh

# Both products: verify the matrix, replay all scenarios, then run the browser
# fixtures with their existing explicit exceptions.
parity:
	bash scripts/check-parity-outcomes.sh --run

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
	go test ./host/webassets -run '^TestPlatformEmbeddedText$$' -count=1
	bash scripts/check-public-text.sh
	bash scripts/check-binary.sh

# Retain the existing developer command, now enforcing its findings.
boundary-report:
	bash scripts/split/namescan.sh
	bash scripts/check-public-text.sh
