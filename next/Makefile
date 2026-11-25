# The owner app (web/app) and its checks. The Go service needs none of this:
# it embeds the committed build in web/dist/files.
APP := web/app
NPM := cd $(APP) && npm
STALE := $(APP)/.stale-check

.PHONY: web web-deps web-stale web-check web-real web-accept

web-deps:
	@[ -d $(APP)/node_modules ] || ($(NPM) ci --no-audit --no-fund)

# Rebuild the owner app and pages.css into web/dist/files (commit the result).
web: web-deps
	$(NPM) run build

# Fails when web/dist/files is not what the source builds to.
web-stale: web-deps
	cd $(APP) && SH_WEB_OUT=.stale-check npx vite build --logLevel error
	@diff -r $(STALE) web/dist/files >/dev/null || (echo "web/dist/files is stale: run make web and commit it"; exit 1)
	@echo "web/dist/files is up to date"

# Everything: types, lint, unit tests, the build, the stale check, and every
# catalogue screen at 390 and 1280 against its baseline picture (with axe).
web-check: web-deps
	$(NPM) run typecheck
	$(NPM) run lint
	$(NPM) test
	$(MAKE) web-stale
	$(NPM) run catalogue
	$(NPM) run visual

# Accept new baseline pictures after a deliberate change (look at them first).
web-accept: web-deps
	$(NPM) run catalogue
	$(NPM) run visual:accept

# The real binary on a throwaway database, clicked through at both widths
# (needs the test Postgres in Docker, as go test does). Pictures land in
# web/app/test-results/real.
web-real: web-deps
	$(NPM) run real
