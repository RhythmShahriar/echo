#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Seeds the testbed WordPress: install, the fixture plugins, the owner's
# address. Run by `make up` inside the wp container (as www-data); does
# nothing if the site is installed.
set -eu
cd /var/www/html

for _ in $(seq 1 90); do
	[ -f wp-config.php ] && [ -f wp-includes/version.php ] && break
	sleep 1
done
for _ in $(seq 1 60); do
	php -r 'exit(@mysqli_connect(getenv("WORDPRESS_DB_HOST"), getenv("WORDPRESS_DB_USER"), getenv("WORDPRESS_DB_PASSWORD"), getenv("WORDPRESS_DB_NAME")) ? 0 : 1);' && break
	sleep 1
done

if wp core is-installed 2>/dev/null; then
	echo "testbed: already seeded"
	exit 0
fi

wp core install --url="${TESTBED_URL:-http://localhost:18850}" --title="Bean & Leaf" --admin_user=admin \
	--admin_password=admin --admin_email=owner@shop.example.com --skip-email
wp option update blogdescription "Coffee and tea, roasted to order"
wp plugin activate testbed-forms testbed-shop testbed-shop-sms testbed-stripe
wp plugin delete akismet hello >/dev/null 2>&1 || true
echo "testbed: seeded"
