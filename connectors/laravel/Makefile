# Everything runs in Docker: composer:2 to install, php:8.3-cli to run.
# Echo's shared test vectors sit outside this folder: mount them when present.
CONFORMANCE := $(wildcard $(abspath $(CURDIR)/../../conformance))
DOCKER   := docker run --rm -u $(shell id -u):$(shell id -g) -e COMPOSER_HOME=/tmp/composer -v $(CURDIR):/app -w /app \
	$(if $(CONFORMANCE),-v $(CONFORMANCE):/conformance:ro -e ECHO_CONFORMANCE=/conformance)
COMPOSER := $(DOCKER) composer:2 composer
PHP      := $(DOCKER) php:8.3-cli php

.PHONY: install test analyse lint e2e check

install: ## install dev dependencies
	$(COMPOSER) update --no-interaction --no-progress

test: ## PHPUnit + Testbench against a fake Echo
	$(PHP) vendor/bin/phpunit

analyse: ## Larastan, level 6
	$(PHP) -d memory_limit=1G vendor/bin/phpstan analyse --no-progress

lint: ## php -l on every file
	$(DOCKER) php:8.3-cli sh -c 'find src config tests fixtures/overlay -name "*.php" -print0 | xargs -0 -n1 php -l >/dev/null'

e2e: ## a real Laravel app against Echo's real catcher
	./fixtures/e2e.sh

check: lint analyse test
