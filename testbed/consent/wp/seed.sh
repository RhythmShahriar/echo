#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Installs WordPress once per make up (the database is in tmpfs) and
# sets up both CMP plugins: activated, so their install routines create
# their tables and default banners, with Complianz's wizard marked done
# and the EU as its region. The mu-plugin decides per site which plugin
# runs. Run as www-data inside the wp container.
set -eu
cd /var/www/html
if ! wp core is-installed 2>/dev/null; then
	wp core install --url=https://g01-wp.shop.test --title="Consent testbed" --admin_user=admin \
		--admin_password=admin --admin_email=admin@shop.test --skip-email --quiet
fi
wp plugin activate complianz-gdpr cookie-law-info --quiet
wp eval '
	update_option("cmplz_wizard_completed_once", true);
	if (function_exists("cmplz_update_option_no_hooks")) {
		cmplz_update_option_no_hooks("regions", "eu");
		cmplz_update_option_no_hooks("consent_for_anonymous_stats", "yes");
	}
'
wp rewrite structure '/%postname%/' --quiet
wp post create --post_type=page --post_title="Page 2" --post_name=page-2 --post_status=publish --quiet >/dev/null
echo "wp: seeded"
