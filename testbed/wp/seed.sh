#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Installs WordPress and the testbed plugins on a fresh site (prod-wp or
# staging-wp). Run by make up inside the container as www-data; does
# nothing when the site is installed. The site's URL is TESTBED_URL.
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

# WordPress verifies TLS with its own bundle: add the stubs' CA so the
# shop's API calls reach the stubs (and are recorded) instead of failing.
if [ -f /stubs/ca.pem ] && ! grep -q "Roadie testbed" wp-includes/certificates/ca-bundle.crt 2>/dev/null; then
	{ echo; echo "Roadie testbed stubs CA"; cat /stubs/ca.pem; } >> wp-includes/certificates/ca-bundle.crt
fi

if wp core is-installed 2>/dev/null; then
	echo "testbed: $(hostname) already seeded"
	exit 0
fi
wp core install --url="$TESTBED_URL" --title="Bean & Leaf Coffee" --admin_user=shopowner \
	--admin_password="$(head -c 18 /dev/urandom | base64)" --admin_email=owner@shop.example.com --skip-email
wp plugin activate woocommerce wp-mail-smtp wp-mail-logging fluent-smtp testbed-subscriptions
wp plugin delete akismet hello >/dev/null 2>&1 || true
wp option update woocommerce_custom_orders_table_enabled yes
echo "testbed: $(hostname) seeded"
