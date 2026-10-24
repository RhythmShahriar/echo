# Everything runs in Docker by default: composer:2 to install,
# php:$(PHP_VERSION)-cli to run. CI runs the same targets natively with
# COMPOSER=composer PHP=php.
#
# COMPOSER_CACHE_DIR (from the environment, default ~/.cache/composer) is
# the download cache, kept between runs and mounted into Docker, so CI can
# cache it. COMPOSER_DISABLE_NETWORK=1 is passed through to install from it
# alone.
PHP_VERSION        ?= 8.3
COMPOSER_CACHE_DIR ?= $(HOME)/.cache/composer
export COMPOSER_CACHE_DIR

# Echo's shared test vectors and wire schema sit outside this folder: mount
# them when present.
CONFORMANCE := $(wildcard $(abspath $(CURDIR)/../../conformance))
SCHEMA      := $(wildcard $(abspath $(CURDIR)/../../wire/schema.json))
DOCKER   := docker run --rm -u $(shell id -u):$(shell id -g) -e COMPOSER_HOME=/tmp/composer \
	-v $(COMPOSER_CACHE_DIR):/composer-cache -e COMPOSER_CACHE_DIR=/composer-cache -e COMPOSER_DISABLE_NETWORK \
	-v $(CURDIR):/app -w /app \
	$(if $(CONFORMANCE),-v $(CONFORMANCE):/conformance:ro -e ECHO_CONFORMANCE=/conformance) \
	$(if $(SCHEMA),-v $(SCHEMA):/wire-schema.json:ro -e ECHO_WIRE_SCHEMA=/wire-schema.json)
COMPOSER := $(DOCKER) composer:2 composer
PHP      := $(DOCKER) php:$(PHP_VERSION)-cli php

.PHONY: install test phpunit analyse lint e2e check

install: ## install dev dependencies (composer.lock if present, else the newest that fit)
	mkdir -p $(COMPOSER_CACHE_DIR)
	$(COMPOSER) install --no-interaction --no-progress

test: install phpunit analyse ## what CI runs: install, PHPUnit, Larastan

phpunit: ## PHPUnit + Testbench against a fake Echo
	$(PHP) vendor/bin/phpunit

analyse: ## Larastan, level 6
	$(PHP) -d memory_limit=1G vendor/bin/phpstan analyse --no-progress

lint: ## php -l on every file
	$(DOCKER) php:$(PHP_VERSION)-cli sh -c 'find src config tests fixtures/overlay -name "*.php" -print0 | xargs -0 -n1 php -l >/dev/null'

e2e: ## a real Laravel app against Echo's conformance stand-in
	./fixtures/e2e.sh

check: lint test
