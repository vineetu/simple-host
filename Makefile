# check-docs-sync.sh needs python3 with PyYAML; check-fresh-install.sh needs a
# local postgres superuser (it creates and drops a throwaway database).
# check-prices-age.sh only warns: the cost calculator's prices are checked monthly.
.PHONY: check

check:
	@test -z "$$(gofmt -l internal/ cmd/ db/)" || { echo "gofmt: $$(gofmt -l internal/ cmd/ db/)"; exit 1; }
	go build ./...
	go vet ./...
	go test ./...
	node --test scripts/tests/arrival.test.cjs scripts/tests/photo-process.test.cjs scripts/tests/my-sites.test.cjs scripts/tests/site-preview.test.cjs
	bash scripts/check-html.sh
	bash scripts/check-layering.sh
	bash scripts/check-docs-sync.sh
	bash scripts/check-claude-plugin.sh
	bash deploy/cert-issuers/runtime_test.sh
	python3 deploy/cert-issuers/requeue_test.py
	bash deploy/cert-issuers/watch_test.sh
	bash deploy/cert-issuers/fallback_test.sh
	python3 deploy/cert-issuers/google_pacing_test.py
	bash deploy/domain-certs/issue_test.sh
	bash deploy/family-certs/issue_test.sh
	bash deploy/family-certs/nginx_fixture_test.sh
	bash deploy/prod/family-adopt_test.sh
	bash deploy/site-certs/site-certs_test.sh
	bash deploy/prod/nginx-suspended-marker_test.sh
	bash deploy/prod/nginx-internal-lock_test.sh
	bash deploy/prod/nginx-sso-log_test.sh
	bash deploy/prod/nginx-analytics-logformat-apply_test.sh
	bash deploy/prod/sh-network-watch_test.sh
	bash deploy/prod/sh-uptime-watch_test.sh
	bash deploy/prod/sh-parity-watch_test.sh
	bash deploy/prod/nginx-site-base-domain_test.sh
	bash deploy/install/install_test.sh
	bash deploy/hack/standalone/tests/install_test.sh
	bash deploy/compose/Caddyfile_test.sh
	bash deploy/digitalocean/droplet/test/pins_test.sh
	bash scripts/check-fresh-install.sh
	@bash scripts/check-prices-age.sh || echo "WARNING: the cost calculator's prices need their monthly check (not a failure)"
