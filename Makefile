GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

.PHONY: build test lint fmt check hooks testbed testbed-down bench

build: ## build bin/rhythm-echo
	go build -o bin/rhythm-echo ./cmd/rhythm-echo

test: ## run tests (browser tests need Chrome)
	go test -race ./...

lint:
	$(GOLANGCI) run ./...

fmt:
	gofmt -w .

check: lint test ## everything CI runs

hooks: ## enable the Conventional Commits hook
	git config core.hooksPath .githooks

testbed: ## start the testbed (WordPress, Laravel, Next.js, plain PHP) in Docker on 127.0.0.1:18850-18855
	$(MAKE) -C testbed up

testbed-down:
	$(MAKE) -C testbed down

bench: build ## run the benchmark against the testbed; writes docs/benchmark.md
	go run -tags testbed ./testbed/bench -bin $(CURDIR)/bin/rhythm-echo
