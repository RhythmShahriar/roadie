#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Installs Roadie for the GitHub Action (action.yml).
#
#   action-install.sh check     picks the version and says whether it's installed
#   action-install.sh install   downloads, verifies and installs that version
#
# check reads VERSION (the version input) and ACTION_REF (github.action_ref)
# and writes version=X.Y.Z and skip=true|false to $GITHUB_OUTPUT. With no
# version input it installs the release that matches the Action's own tag,
# so RhythmShahriar/roadie@v1.2.3 always runs Roadie 1.2.3 and a new
# release can't change a pinned workflow's results. A branch or a major tag
# such as @v1 doesn't name a release; then it takes the latest one and
# warns. Every use of the Action in a job installs into the same folder,
# so a later step finds the version already there and skips the download
# (and the cosign install before it).
#
# install reads VERSION (the X.Y.Z from check). It downloads the archive,
# checksums.txt and its sigstore bundle and runs verify-release.sh before
# anything from the archive runs.
set -euo pipefail

repo=https://github.com/RhythmShahriar/roadie
dir="${RUNNER_TEMP:?}/roadie-bin"

# exe is the binary's suffix: .exe on Windows runners (Git Bash).
exe() {
	case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN* | Windows_NT) printf '.exe' ;;
	esac
}

# latest asks GitHub which release /releases/latest redirects to.
latest() {
	local url
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || {
		echo "::error::couldn't look up Roadie's latest release at $repo/releases/latest" >&2
		return 1
	}
	case "$url" in
	*/releases/tag/v*) printf '%s\n' "${url##*/releases/tag/v}" ;;
	*)
		echo "::error::couldn't tell Roadie's latest release from $url" >&2
		return 1
		;;
	esac
}

# installed prints the version of the Roadie already in $dir, if any: the
# second word of rhythm-roadie --version.
installed() {
	local bin
	bin="$dir/rhythm-roadie$(exe)"
	[ -x "$bin" ] || return 0
	"$bin" --version 2>/dev/null | awk '{print $2; exit}'
}

check() {
	local v=${VERSION:-} ref=${ACTION_REF:-}
	if [ -z "$v" ]; then
		if [[ $ref =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
			v=$ref
		else
			echo "::warning::Roadie's version isn't pinned: the action ref \"${ref:-none}\" isn't a release tag such as v1.2.3, so the latest release is installed and a new one can change your results. Use the Action at a release tag or set the version input."
			v=latest
		fi
	fi
	if [ "$v" = latest ]; then v=$(latest); fi
	v=${v#v}
	[[ $v =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || {
		echo "::error::version \"$v\" isn't a Roadie version such as 1.2.3 or latest"
		exit 2
	}
	local skip=false
	if [ "$(installed)" = "$v" ]; then
		skip=true
		echo "Roadie $v is already installed; skipping the download."
		echo "$dir" >>"${GITHUB_PATH:?}"
	fi
	{
		echo "version=$v"
		echo "skip=$skip"
	} >>"${GITHUB_OUTPUT:?}"
}

install() {
	local v=${VERSION:?} arch os ext archive here
	here=$(cd "$(dirname "$0")" && pwd)
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*)
		echo "::error::Roadie has no release for this CPU ($(uname -m))"
		exit 1
		;;
	esac
	case "$(uname -s)" in
	Linux) os=linux ext=tar.gz ;;
	Darwin) os=darwin ext=tar.gz ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT) os=windows ext=zip ;;
	*)
		echo "::error::Roadie has no release for this system ($(uname -s))"
		exit 1
		;;
	esac
	archive="rhythm-roadie_${os}_${arch}.$ext"
	local base="$repo/releases/download/v$v"
	rm -rf "$dir"
	mkdir -p "$dir"
	cd "$dir"
	curl -fsSLO "$base/$archive"
	curl -fsSLO "$base/checksums.txt"
	curl -fsSLO "$base/checksums.txt.sigstore.json"
	# The signature, then the checksum: see verify-release.sh.
	"$here/verify-release.sh" . "$archive" "$v"
	local bin
	bin="rhythm-roadie$(exe)"
	if [ "$ext" = zip ]; then
		unzip -q -o "$archive" "$bin"
	else
		tar -xzf "$archive" "$bin"
	fi
	rm -f "$archive" checksums.txt checksums.txt.sigstore.json
	"./$bin" --version
}

case "${1:-}" in
check) check ;;
install)
	install
	echo "$dir" >>"${GITHUB_PATH:?}"
	;;
*)
	echo "usage: action-install.sh check|install" >&2
	exit 2
	;;
esac
