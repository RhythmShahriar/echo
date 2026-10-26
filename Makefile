GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

IMAGE_ARCH ?= $(shell go env GOARCH)

.PHONY: build image test test-connectors test-docker test-browser test-integrated fuzz-consent bench-consent testbed-consent testbed-consent-down actionlint lint fmt check hooks testbed testbed-down bench

build: ## build bin/rhythm-echo
	go build -o bin/rhythm-echo ./cmd/rhythm-echo

image: ## build the container image for this machine as rhythm-echo:dev (needs Docker with buildx)
	rm -rf bin/image && mkdir -p bin/image/linux/$(IMAGE_ARCH)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(IMAGE_ARCH) go build -trimpath -o bin/image/linux/$(IMAGE_ARCH)/rhythm-echo ./cmd/rhythm-echo
	docker buildx build --load --platform linux/$(IMAGE_ARCH) -t rhythm-echo:dev -f Dockerfile bin/image

test: ## run the Go tests (the connector suites: make test-connectors)
	go test -race ./...

test-connectors: ## run the Node, Laravel and WordPress connector suites (needs Node and Docker)
	cd connectors/node && npm ci --ignore-scripts && npm run typecheck && npm test
	$(MAKE) -C connectors/laravel install test
	go test -tags docker ./connectors/wordpress/

test-docker: ## run every Docker test, as the nightly CI does
	go test -tags docker -timeout 20m ./...

test-browser: ## run the consent tests that start Chrome (they skip when there is none)
	go test -tags browser -timeout 20m ./consent/... ./internal/browser/... ./testbed/consent/...

test-integrated: ## run the tests with every consent stage required, as the nightly CI and the release do
	ECHO_CONSENT_REQUIRE_LINKED=1 go test -race ./...

FUZZTIME ?= 30s

fuzz-consent: ## run every consent fuzz target for FUZZTIME each (default 30s)
	scripts/fuzz-consent.sh $(FUZZTIME)

actionlint: ## lint the GitHub workflows, from the image CI pins
	docker run --rm -v "$(CURDIR):/repo" -w /repo rhysd/actionlint@sha256:887a259a5a534f3c4f36cb02dca341673c6089431057242cdc931e9f133147e9 -color

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

testbed-consent: ## start the consent testbed in Docker on 127.0.0.1:18950-18969
	$(MAKE) -C testbed/consent up

testbed-consent-down:
	$(MAKE) -C testbed/consent down

bench-consent: ## run the consent benchmark against the consent testbed; writes docs/benchmark-consent.md
	$(MAKE) build && $(MAKE) -C testbed/consent bench BIN=$(CURDIR)/bin/rhythm-echo
