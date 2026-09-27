# check-docs-sync.sh needs python3 with PyYAML; check-fresh-install.sh needs a
# local postgres superuser (it creates and drops a throwaway database).
.PHONY: check

check:
	@test -z "$$(gofmt -l internal/ cmd/ db/)" || { echo "gofmt: $$(gofmt -l internal/ cmd/ db/)"; exit 1; }
	go build ./...
	go vet ./...
	go test ./...
	bash scripts/check-html.sh
	bash scripts/check-layering.sh
	bash scripts/check-docs-sync.sh
	bash scripts/check-claude-plugin.sh
	bash deploy/domain-certs/issue_test.sh
	bash deploy/prod/nginx-suspended-marker_test.sh
	bash deploy/prod/nginx-internal-lock_test.sh
	bash deploy/prod/nginx-analytics-logformat-apply_test.sh
	bash scripts/check-fresh-install.sh
