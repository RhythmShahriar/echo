# The Echo consent testbed. Go mode (no Docker): make serve, then run
# rhythm-echo consent with the flags make map prints. Sealed Docker mode
# (the benchmark of record): make up, make bench, make down. Ports: Go
# mode 127.0.0.1:23400-23439, Docker 127.0.0.1:18950-18959.

ROOT := $(CURDIR)
REPO := $(abspath $(CURDIR)/../..)
GO ?= go
SERVER := $(GO) run ./testbed/consent/server/cmd/consent-testbed

BIN ?= $(REPO)/bin/rhythm-echo
# The benchmark of record runs in the sealed Docker testbed (make up);
# MODE=go runs it against the Go server in the benchmark's own process.
MODE ?= docker
BENCH := $(GO) run -tags testbed ./testbed/consent/bench

COMPOSE := docker compose -f docker-compose.yml
ARCH ?= $(shell $(GO) env GOARCH)
LINUX := CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "-s -w"
export TESTBED_UID := $(shell id -u)
export TESTBED_GID := $(shell id -g)

.PHONY: ca serve map toggle bench reproduce runtime up down

ca: ## make the testbed CA in ca/ (git-ignored; delete the folder for a new one)
	cd $(REPO) && $(SERVER) -make-ca $(ROOT)/ca

serve: ## serve every scenario in Go mode on 127.0.0.1:23400 (http and https) and 23401 (admin)
	@test -f ca/ca.pem || $(MAKE) ca
	cd $(REPO) && $(SERVER) -root $(ROOT) -tls

map: ## print the --map-host flags rhythm-echo needs for Go mode
	@cd $(REPO) && $(SERVER) -print-map

toggle: ## switch a scenario: make toggle ID=c01 STATE=broken
	./toggle.sh $(ID) $(STATE)

bench: ## run the benchmark (MODE=docker after make up, or MODE=go); writes docs/benchmark-consent.md and bench/work/results.json
	cd $(REPO) && $(BENCH) -bin $(BIN) -mode $(MODE) $(ARGS)

reproduce: ## two full benchmark runs, then -diff: fails when any outcome differs
	cd $(REPO) && $(BENCH) -bin $(BIN) -mode $(MODE) -work $(ROOT)/bench/work-a -out $(ROOT)/bench/work-a.md $(ARGS); \
		a=$$?; $(BENCH) -bin $(BIN) -mode $(MODE) -work $(ROOT)/bench/work-b -out $(ROOT)/bench/work-b.md $(ARGS); \
		b=$$?; if [ $$a -gt 1 ] || [ $$b -gt 1 ]; then exit 3; fi; \
		$(BENCH) -diff $(ROOT)/bench/work-a/results.json $(ROOT)/bench/work-b/results.json

runtime: ## build what the containers run into runtime/: rhythm-echo (release flags), peakmem, the testbed server, the resolver and the gate certificates
	@test -f ca/ca.pem || $(MAKE) ca
	mkdir -p runtime/linux runtime/gate state
	cd $(REPO) && $(LINUX) -o $(ROOT)/runtime/linux/rhythm-echo ./cmd/rhythm-echo
	cd $(REPO) && $(LINUX) -o $(ROOT)/runtime/linux/peakmem ./testbed/consent/bench/peakmem
	cd $(REPO) && $(LINUX) -o $(ROOT)/runtime/linux/consent-testbed ./testbed/consent/server/cmd/consent-testbed
	cd $(REPO) && $(LINUX) -o $(ROOT)/runtime/linux/dns ./testbed/consent/dns
	cp runtime/linux/rhythm-echo runtime/rhythm-echo
	cd $(REPO) && $(SERVER) -root $(ROOT) -gate-certs $(ROOT)/runtime/gate

up: runtime ## build and start the sealed testbed, then set up WordPress (fresh every time)
	$(COMPOSE) build -q wp runner
	$(COMPOSE) --profile echo up -d --wait gate dns shop tags db wp echo runner
	$(COMPOSE) exec -T -u www-data wp sh /seed.sh
	@echo "Sites: https://<id>.shop.test through 127.0.0.1:18950; sinks 127.0.0.1:18951 (tags) and 18952 (sites)"

down: ## stop and delete everything, volumes included; runtime/ and ca/ stay
	$(COMPOSE) --profile test --profile echo down -v --remove-orphans
