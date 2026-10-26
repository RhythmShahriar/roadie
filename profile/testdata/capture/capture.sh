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

# The free form, mail-log and shop plugins, pinned. Each can be changed
# from the environment (CF7_VERSION=6.1 capture.sh); a version that
# WordPress.org no longer serves stops the run.
CF7_VERSION=${CF7_VERSION:-6.1}
FLAMINGO_VERSION=${FLAMINGO_VERSION:-2.5}
NINJA_VERSION=${NINJA_VERSION:-3.10.2.1}
FLUENTFORM_VERSION=${FLUENTFORM_VERSION:-6.0.4}
WPFORMS_VERSION=${WPFORMS_VERSION:-1.9.6.2}
WPMAILSMTP_VERSION=${WPMAILSMTP_VERSION:-4.5.0}
POSTSMTP_VERSION=${POSTSMTP_VERSION:-3.2.0}
FLUENTSMTP_VERSION=${FLUENTSMTP_VERSION:-2.2.90}
WPML_VERSION=${WPML_VERSION:-1.14.0}
EMAILLOG_VERSION=${EMAILLOG_VERSION:-2.61}
MAILPOET_VERSION=${MAILPOET_VERSION:-5.12.0}
EDD_VERSION=${EDD_VERSION:-3.3.9}

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

# Activating a plugin creates its tables. Nothing is configured: only the
# schema is captured from them.
for p in "contact-form-7:$CF7_VERSION" "flamingo:$FLAMINGO_VERSION" "ninja-forms:$NINJA_VERSION" \
	"fluentform:$FLUENTFORM_VERSION" "wpforms-lite:$WPFORMS_VERSION" "wp-mail-smtp:$WPMAILSMTP_VERSION" \
	"post-smtp:$POSTSMTP_VERSION" "fluent-smtp:$FLUENTSMTP_VERSION" "wp-mail-logging:$WPML_VERSION" \
	"email-log:$EMAILLOG_VERSION" "mailpoet:$MAILPOET_VERSION" "easy-digital-downloads:$EDD_VERSION"; do
	wp plugin install "${p%%:*}" --version="${p#*:}" --activate
done

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

split "ninja-forms-$NINJA_VERSION" '^nf3_'
split "fluent-forms-$FLUENTFORM_VERSION" '^fluentform_'
split "wpforms-lite-$WPFORMS_VERSION" '^wpforms_'
split "mailpoet-$MAILPOET_VERSION" '^mailpoet_'
split "edd-$EDD_VERSION" '^edd_'
split "mail-logs" '^(wpmailsmtp_|post_smtp_|fsmpt_|wpml_mails$|email_log$)'

# The hand-written *-docs.sql files stay for the paid plugins (Gravity
# Forms, WPForms Pro, WP Mail SMTP Pro, MemberPress, LearnDash); point
# schemaFiles in ../../profiles_test.go at the captured files for the rest.
echo "capture: wrote $schemas; run go test ./profile to check the profiles against them"
