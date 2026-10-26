#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Fetches the other tools the benchmark judges (testbed/tools/README.md)
# into <dir>/<name>/: the release as "release" (the file as published),
# "version" plus "not-run" with the reason when it can't be used. Each
# line of TESTBED_TOOLS is "<name> <version> <url> <sha256>". A sha256 of
# "unverified" is never downloaded: nothing runs here unchecked. A failed
# download or a wrong sha256 also gives "not-run" instead of failing make
# up, so the benchmark reports the tool as not run with the reason.
#
#   TESTBED_TOOLS="$(TOOLS)" tools/fetch.sh runtime/tools
set -eu

dir=${1:?usage: fetch.sh <dir>}
mkdir -p "$dir"

printf '%s\n' "${TESTBED_TOOLS:-}" | while read -r name version url sum; do
	[ -n "$name" ] || continue
	out="$dir/$name"
	rm -rf "$out"
	mkdir -p "$out"
	printf '%s\n' "$version" >"$out/version"
	if [ "$sum" = unverified ]; then
		printf 'no sha256 pinned for %s %s in testbed/Makefile yet\n' "$name" "$version" >"$out/not-run"
		echo "tools: $name $version not fetched (no sha256 pinned)"
		continue
	fi
	if ! curl -fsSL --max-time 300 -o "$out/release.part" "$url"; then
		printf 'the download of %s failed\n' "$url" >"$out/not-run"
		rm -f "$out/release.part"
		echo "tools: $name $version not fetched (download failed)"
		continue
	fi
	got=$(sha256sum "$out/release.part" | cut -d' ' -f1)
	if [ "$got" != "$sum" ]; then
		printf 'the sha256 of %s is %s, not the pinned %s\n' "$url" "$got" "$sum" >"$out/not-run"
		rm -f "$out/release.part"
		echo "tools: $name $version not fetched (sha256 mismatch)"
		continue
	fi
	mv "$out/release.part" "$out/release"
	echo "tools: $name $version"
done
