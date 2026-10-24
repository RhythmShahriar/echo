GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

IMAGE_ARCH ?= $(shell go env GOARCH)

.PHONY: build image test test-connectors test-docker actionlint lint fmt check hooks testbed testbed-down bench

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
