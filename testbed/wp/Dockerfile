# WordPress with WP-CLI inside, so actions and flushes run with a quick
# `docker exec` (testbed/bin/wp) instead of a new container each time.
FROM wordpress:cli-2.12.0-php8.3 AS cli
FROM wordpress:6.8.3-php8.3-apache
COPY --from=cli /usr/local/bin/wp /usr/local/bin/wp
