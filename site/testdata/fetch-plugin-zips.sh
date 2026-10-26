#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Fetches the plugin zips the nightly docker tests install into one
# folder, each checked against its pinned SHA-256. The tests never
# download; run this in a step of its own, then the tests with
# ROADIE_PLUGIN_ZIPS=<folder>. Keep the pins in step with
# site/integration_docker_test.go and site/killswitch/muplugin_docker_test.go.
#
# Usage: fetch-plugin-zips.sh <folder>
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: fetch-plugin-zips.sh <folder>" >&2
	exit 2
fi
dir=$1
mkdir -p -- "$dir"

# name url sha256
pins='woocommerce.9.8.5.zip https://downloads.wordpress.org/plugin/woocommerce.9.8.5.zip 9008e72eacda8f3bda01d0b704502eaddd8bc147651d61bd9aa7a015bf583964
wp-mail-smtp.4.10.3.zip https://downloads.wordpress.org/plugin/wp-mail-smtp.4.10.3.zip 39fdb36add56c42ae803684cf94053320e4ba1262d24933d2eb16c215266c6a4
woocommerce-subscriptions-core.8.2.0.zip https://github.com/Automattic/woocommerce-subscriptions-core/archive/refs/tags/8.2.0.zip 1d2a898b65eb76473558d797407b3e41a0a3cbf898c4a2e5ae24e1df4ccf893e'

echo "$pins" | while read -r name url sum; do
	out=$dir/$name
	if [ ! -f "$out" ]; then
		curl -fsSL --retry 3 -o "$out.part" -- "$url"
		mv -f -- "$out.part" "$out"
	fi
	got=$(sha256sum -- "$out" | cut -d ' ' -f 1)
	if [ "$got" != "$sum" ]; then
		echo "$name: sha256 $got, want $sum" >&2
		rm -f -- "$out"
		exit 1
	fi
	echo "$name ok"
done
