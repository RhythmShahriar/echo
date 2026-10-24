# Echo testbed: 20 scenarios of a site that stops saying what it should, on
# WordPress, Laravel, Next.js and plain PHP, behind a network with no way out,
# and 15 of the environment around Echo going wrong.
# Needs Docker with Compose v2 and Go (to build Echo from this checkout).
# Ports: sites 18850-18858 on 127.0.0.1, Echo 18860-18862 (on the host),
# the local environment probes 23200-23239.
#
# TOOLS names what the benchmark judges besides Echo, as
# make up benchmark TOOLS="echo mailpit buggregator staging-shield":
# buggregator starts Buggregator, staging-shield bakes the Staging Shield
# plugin into the WordPress image (downloaded from wordpress.org).

COMPOSE  := docker compose -f docker-compose.yml
GO       ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
RUNTIME  := runtime
ECHO     := $(RUNTIME)/rhythm-echo
TOKEN    ?= echo-testbed-token
# Echo keeps its proxy CA in the user cache dir; the testbed keeps its own.
ECHO_ENV := XDG_CACHE_HOME=$(CURDIR)/$(RUNTIME)/cache RHYTHM_ECHO_TOKEN=$(TOKEN) RHYTHM_ECHO_URL=http://127.0.0.1:18860

export TESTBED_UID := $(shell id -u)
export TESTBED_GID := $(shell id -g)
export TESTBED_TOKEN := $(TOKEN)

TOOLS ?= echo mailpit
export COMPOSE_PROFILES := $(if $(filter buggregator,$(TOOLS)),buggregator)
# The versions of the other tools the benchmark is run with, so two runs
# (or a run from a clean checkout) judge the same releases. testbed/bench
# names them in docs/benchmark.md and checks they match.
export BUGGREGATOR_VERSION ?= 2.4.1
export STAGING_SHIELD ?= $(if $(filter staging-shield,$(TOOLS)),1.0.2)
WORK  := bench/work
VHS   ?= vhs

.PHONY: up down reset good broken status logs wp echo binaries ca connector apps benchmark reproduce demo

up: binaries ca connector apps ## build, start and seed everything (WordPress is fresh every time)
	@mkdir -p state
	$(COMPOSE) build -q wp
	$(COMPOSE) up -d --wait
	docker exec -u www-data echo-testbed-wp-1 sh /seed.sh
	@for i in $$(seq 1 60); do \
		curl -fsS -o /dev/null http://localhost:18850/ && curl -fsS -o /dev/null http://localhost:18851/ && \
		curl -fsS -o /dev/null http://localhost:18852/ && curl -fsS -o /dev/null http://localhost:18853/ && break; sleep 1; done
	@echo "WordPress: http://localhost:18850/  (admin/admin)"
	@echo "Laravel:   http://localhost:18851/"
	@echo "Next.js:   http://localhost:18852/"
	@echo "Plain PHP: http://localhost:18853/"
	@echo "Mailpit:   http://localhost:18854/"
	@echo "Echo isn't started by make up: the benchmark starts its own, or run: make echo"
	@./toggle.sh status

down: ## stop and delete everything (containers, database, WordPress volume); built apps stay in runtime/
	COMPOSE_PROFILES=buggregator $(COMPOSE) down -v --remove-orphans

reset: down up ## start over from a freshly seeded site

good: ## make every scenario good, or one: make good ID=s05
	./toggle.sh $(or $(ID),all) good

broken: ## break one scenario (make broken ID=s05), or every one when ID is unset
	./toggle.sh $(or $(ID),all) broken

status: ## list broken scenarios
	@./toggle.sh status

logs: ## follow the sites' logs
	$(COMPOSE) logs -f wp laravel next php crm

wp: ## run WP-CLI, e.g. make wp ARGS="option get admin_email"
	bin/wp $(ARGS)

echo: binaries ca ## run Echo in the foreground the way the benchmark does (sealed; OBSERVE=1 for observe mode)
	$(ECHO_ENV) $(ECHO) up --config .echo/config.yaml --token $(TOKEN) $(if $(OBSERVE),--observe,--seal)

benchmark: binaries ## run every scenario for Echo and TOOLS; writes ../docs/benchmark.md and bench/work/results.json
	cd .. && GOWORK=off $(GO) run -tags testbed ./testbed/bench -bin testbed/$(ECHO) -tools "$(TOOLS)"

reproduce: binaries ## run the benchmark twice and fail if any outcome differs
	cd .. && GOWORK=off $(GO) run -tags testbed ./testbed/bench -bin testbed/$(ECHO) -tools "$(TOOLS)" -work testbed/$(WORK)/first -out testbed/$(WORK)/first.md
	cd .. && GOWORK=off $(GO) run -tags testbed ./testbed/bench -bin testbed/$(ECHO) -tools "$(TOOLS)" -work testbed/$(WORK)/second -out testbed/$(WORK)/second.md
	cd .. && GOWORK=off $(GO) run -tags testbed ./testbed/bench -diff testbed/$(WORK)/first/results.json testbed/$(WORK)/second/results.json

demo: binaries ca ## render the 60-second demo (demo/demo.tape) to ../docs/assets/demo.gif; needs make up and VHS (charmbracelet/vhs)
	mkdir -p ../docs/assets && cd demo && $(VHS) demo.tape

# Echo from this checkout: one binary for the host (the CLI, the benchmark's
# default -bin is ../bin/rhythm-echo) and a static Linux one for the
# containers (the sendmail shim).
binaries:
	@mkdir -p $(RUNTIME)
	cd .. && GOWORK=off $(GO) build -o testbed/$(ECHO) ./cmd/rhythm-echo
	cd .. && CGO_ENABLED=0 GOOS=linux GOWORK=off $(GO) build -o testbed/$(RUNTIME)/rhythm-echo-linux ./cmd/rhythm-echo

# The proxy's CA certificate (never its key), mounted into WordPress.
ca:
	@$(ECHO_ENV) $(ECHO) ca --pem > $(RUNTIME)/ca.pem.tmp && mv $(RUNTIME)/ca.pem.tmp $(RUNTIME)/ca.pem

# Echo's WordPress connector, into the mounted mu-plugins (gitignored).
connector:
	$(ECHO) install wordpress wp --url http://gate:18860 --token $(TOKEN) \
		--proxy http://echo:$(TOKEN)@gate:18862 --ca /echo-ca/ca.pem

apps:
	laravel/build.sh
	next/build.sh
