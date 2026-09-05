# check-docs-sync.sh needs python3 with PyYAML; check-fresh-install.sh needs a
# local postgres superuser (it creates and drops a throwaway database).
.PHONY: check

check:
	@test -z "$$(gofmt -l internal/ cmd/)" || { echo "gofmt: $$(gofmt -l internal/ cmd/)"; exit 1; }
	go build ./...
	go vet ./...
	go test ./...
	bash scripts/check-layering.sh
	bash scripts/check-docs-sync.sh
	bash scripts/check-fresh-install.sh
