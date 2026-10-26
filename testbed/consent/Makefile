# The Echo consent testbed. Go mode (no Docker): make serve, then run
# rhythm-echo consent with the flags make map prints.

ROOT := $(CURDIR)
REPO := $(abspath $(CURDIR)/../..)
GO ?= go
SERVER := $(GO) run ./testbed/consent/server/cmd/consent-testbed

.PHONY: ca serve map toggle

ca: ## make the testbed CA in ca/ (git-ignored; delete the folder for a new one)
	cd $(REPO) && $(SERVER) -make-ca $(ROOT)/ca

serve: ## serve every scenario in Go mode on 127.0.0.1:23400 (http and https) and 23401 (admin)
	@test -f ca/ca.pem || $(MAKE) ca
	cd $(REPO) && $(SERVER) -root $(ROOT) -tls

map: ## print the --map-host flags rhythm-echo needs for Go mode
	@cd $(REPO) && $(SERVER) -print-map

toggle: ## switch a scenario: make toggle ID=c01 STATE=broken
	./toggle.sh $(ID) $(STATE)
