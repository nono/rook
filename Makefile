.PHONY: check test lint

check: lint test

test:
	go test -race ./...

# golangci-lint runs gofmt and go vet too (see .golangci.yml).
# deadcode exits 0 on findings, so its output is the failure.
lint:
	golangci-lint run
	govulncheck ./...
	go mod tidy -diff
	@out=$$(deadcode -test ./...) && [ -z "$$out" ] || { echo "$$out"; exit 1; }
	jactionlint
