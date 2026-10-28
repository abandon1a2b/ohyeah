VERSION ?= 0.1.0-dev

.PHONY: build web test check contracts e2e audit

build: web
	go build -trimpath -ldflags "-X github.com/abandon1a2b/ohyeah/internal/cli.version=$(VERSION)" -o ohyeah .

web:
	npm run build

test: check

contracts:
	go run ./internal/contracts

check:
	go run ./internal/contracts --check
	npm run check
	go vet ./...
	go test -race ./...

e2e: build
	npm run test:e2e

audit:
	npm audit --audit-level=low
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
