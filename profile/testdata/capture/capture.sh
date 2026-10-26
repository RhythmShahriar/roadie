#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Captures the database schema of the plugins the built-in profiles
# cover, at pinned versions, into ../schemas/<plugin>-<version>.sql.
#
# It needs Docker and the network (WordPress.org and GitHub downloads),
# so CI never runs it; its outputs are committed. Run it from anywhere:
#
#   profile/testdata/capture/capture.sh
#
# The sample customers and orders it creates use example.com addresses
# and the fictional +44 7700 900xxx range. The full dump with those rows
# goes to out/full.sql (git-ignored) for a look; the committed files hold
# the schema only, so a diff shows a schema change and nothing else.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
schemas="$here/../schemas"
out="$here/out"
mkdir -p "$out"

WP_VERSION=6.8
WC_VERSION=9.8.0
AS_VERSION=3.9.2 # the copy WooCommerce 9.8.0 bundles
WCS_CORE_VERSION=${WCS_CORE_VERSION:-7.9.0}

compose() { docker compose -f "$here/compose.yaml" "$@"; }
wp() { compose run --rm -T cli "$@"; }

cleanup() { compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

compose up -d --wait db wordpress

wp core install --url=http://127.0.0.1:33020 --title="Roadie capture" \
	--admin_user=admin --admin_email=admin@example.com --admin_password=capture --skip-email
wp plugin install woocommerce --version="$WC_VERSION" --activate
wp plugin install \
	"https://github.com/Automattic/woocommerce-subscriptions-core/releases/download/$WCS_CORE_VERSION/woocommerce-subscriptions-core.zip" \
	--activate || echo "capture: woocommerce-subscriptions-core $WCS_CORE_VERSION not installed; its rules use core tables only" >&2

# HPOS on with compatibility mode, so both order stores have rows.
wp wc hpos enable --with-sync || wp option update woocommerce_custom_orders_table_enabled yes

i=1
while [ "$i" -le 3 ]; do
	wp user create "customer$i" "customer$i@example.com" --role=customer \
		--first_name="Sample$i" --last_name="Customer"
	wp user meta update "customer$i" billing_phone "+44 7700 90012$i"
	wp wc shop_order create --user=admin --customer_id="$((i + 1))" --status=processing \
		--billing="{\"first_name\":\"Sample$i\",\"last_name\":\"Customer\",\"email\":\"customer$i@example.com\",\"phone\":\"+44 7700 90012$i\",\"city\":\"Leeds\",\"postcode\":\"LS1 4AP\",\"country\":\"GB\"}"
	i=$((i + 1))
done

# Multisite last: it adds the network tables and blog 2 to the same dump.
wp core multisite-convert --title="Roadie capture network"
wp site create --slug=fr --title="Blog 2" --email=admin@example.com

wp db export --skip-plugins --skip-themes - >"$out/full.sql"
wp db export --skip-plugins --skip-themes --no-data - >"$out/schema.sql"

# split NAME REGEX writes the CREATE TABLE statements of wp_ tables whose
# unprefixed name matches REGEX to ../schemas/NAME.sql.
split() {
	{
		printf -- '-- Captured by capture.sh from a WordPress %s install with WooCommerce %s.\n' "$WP_VERSION" "$WC_VERSION"
		awk -v re="$2" '
			/^CREATE TABLE `/ { name = $3; gsub(/`/, "", name); sub(/^wp_/, "", name); keep = (name ~ re) }
			keep { print }
			keep && /^\) / { keep = 0 }
		' "$out/schema.sql"
	} >"$schemas/$1.sql"
}

split "wordpress-$WP_VERSION" '^(users|usermeta|termmeta|terms|term_taxonomy|term_relationships|commentmeta|comments|links|options|postmeta|posts|blogs|blogmeta|registration_log|site|sitemeta|signups)$'
split "woocommerce-$WC_VERSION" '^(wc_|woocommerce_)'
split "action-scheduler-$AS_VERSION" '^actionscheduler_'

echo "capture: wrote $schemas; run go test ./profile to check the profiles against them"
