# The owner app (../web/host, on the shared kit in ../web/kit) and its checks.
# The Go service needs none of this: it embeds the committed build in web/dist/files.
WEB := ../web
APP := $(WEB)/host
NPM := cd $(WEB) && npm
STALE := $(APP)/.stale-check

.PHONY: web web-deps web-pages web-stale web-check web-real web-accept

web-deps:
	@[ -d $(WEB)/node_modules ] || ($(NPM) ci --no-audit --no-fund)

# Rebuild the owner app and pages.css into web/dist/files (commit the result).
web: web-deps
	$(NPM) run host:build

# The gate and status pages as the Go server writes them, for the catalogue.
web-pages:
	SH_GATE_PAGES=$(abspath $(APP))/.catalogue-pages go test ./sites/core -run TestWriteGatePages -count=1

# Fails when web/dist/files is not what the source builds to.
web-stale: web-deps
	cd $(WEB) && SH_WEB_OUT=.stale-check npx vite build -c host/vite.config.ts --logLevel error
	@diff -r $(STALE) web/dist/files >/dev/null || (echo "web/dist/files is stale: run make web and commit it"; exit 1)
	@echo "web/dist/files is up to date"

# Everything: types, lint, unit tests, the build, the stale check, and every
# catalogue screen at 390 and 1280 against its baseline picture (with axe).
web-check: web-deps web-pages
	$(NPM) run host:typecheck
	$(NPM) run lint
	$(NPM) run host:test
	$(MAKE) web-stale
	$(NPM) run host:catalogue
	$(NPM) run host:visual

# Accept new baseline pictures after a deliberate change (look at them first).
web-accept: web-deps web-pages
	$(NPM) run host:catalogue
	$(NPM) run host:visual:accept

# The real binary on a throwaway database, clicked through at both widths
# (needs the test Postgres in Docker, as go test does). Pictures land in
# ../web/host/test-results/real.
web-real: web-deps
	$(NPM) run host:real
