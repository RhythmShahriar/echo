# The Echo consent testbed. Go mode (no Docker): make serve, then run
# rhythm-echo consent with the flags make map prints.

ROOT := $(CURDIR)
REPO := $(abspath $(CURDIR)/../..)
GO ?= go
SERVER := $(GO) run ./testbed/consent/server/cmd/consent-testbed

BIN ?= $(REPO)/bin/rhythm-echo
# The benchmark of record runs in the sealed Docker testbed (make up);
# MODE=go runs it against the Go server in the benchmark's own process.
MODE ?= docker
BENCH := $(GO) run -tags testbed ./testbed/consent/bench

.PHONY: ca serve map toggle bench reproduce

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
