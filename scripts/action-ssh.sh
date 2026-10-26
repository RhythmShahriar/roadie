#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Sets up and removes the SSH key for the GitHub Action's ssh:// sources
# and targets (the ssh-key and known-hosts inputs).
#
#   action-ssh.sh setup     writes the key and the host keys
#   action-ssh.sh cleanup   removes the key and restores ~/.ssh as it was
#
# setup reads SSH_KEY and KNOWN_HOSTS. It writes the key to
# ~/.ssh/roadie_action_key (0600), appends KNOWN_HOSTS to
# ~/.ssh/known_hosts and adds "IdentityFile ~/.ssh/roadie_action_key"
# under "Host *" at the top of ~/.ssh/config. Host key checking stays on:
# a key without known hosts is an input error, since the first connection
# would otherwise have to trust whatever answers. The files it changes are
# copied to $RUNNER_TEMP/roadie-ssh first, so cleanup (an always() step at
# the end of the Action) can put them back on a runner that outlives the
# job.
set -euo pipefail

ssh_dir="${HOME:?}/.ssh"
key="$ssh_dir/roadie_action_key"
backup="${RUNNER_TEMP:?}/roadie-ssh"

setup() {
	if [ -z "${SSH_KEY:-}" ]; then
		echo "No ssh-key input; Roadie uses the runner's own SSH setup."
		return 0
	fi
	if [ -z "${KNOWN_HOSTS:-}" ]; then
		echo "::error::the ssh-key input needs known-hosts too: the Action never turns host key checking off. Set known-hosts to the output of ssh-keyscan for your hosts, checked by hand, from a secret."
		exit 2
	fi
	# The key reaches the log only masked, line by line.
	local line
	while IFS= read -r line; do
		line=${line%$'\r'}
		[ ${#line} -lt 4 ] || echo "::add-mask::$line"
	done <<<"$SSH_KEY"

	mkdir -p "$ssh_dir" "$backup"
	chmod 700 "$ssh_dir"
	local f
	for f in config known_hosts; do
		if [ -e "$ssh_dir/$f" ]; then
			cp -p "$ssh_dir/$f" "$backup/$f"
		else
			: >"$backup/$f.absent"
		fi
	done

	(
		umask 077
		# A key pasted into a secret often loses its last newline, which
		# OpenSSH needs.
		printf '%s\n' "${SSH_KEY%$'\n'}" | tr -d '\r' >"$key"
	)
	chmod 600 "$key"
	printf '%s\n' "${KNOWN_HOSTS%$'\n'}" | tr -d '\r' >>"$ssh_dir/known_hosts"
	chmod 600 "$ssh_dir/known_hosts"

	# First, so it applies before any Host block already there.
	{
		printf 'Host *\n  IdentityFile ~/.ssh/roadie_action_key\n\n'
		[ ! -e "$backup/config" ] || cat "$backup/config"
	} >"$ssh_dir/config.roadie"
	mv "$ssh_dir/config.roadie" "$ssh_dir/config"
	chmod 600 "$ssh_dir/config"
	echo "Wrote the SSH key to ~/.ssh/roadie_action_key and the host keys to ~/.ssh/known_hosts; the Action removes the key and restores ~/.ssh when it ends."
}

cleanup() {
	rm -f "$key"
	[ -d "$backup" ] || return 0
	local f
	for f in config known_hosts; do
		if [ -e "$backup/$f.absent" ]; then
			rm -f "$ssh_dir/$f"
		elif [ -e "$backup/$f" ]; then
			cp -p "$backup/$f" "$ssh_dir/$f"
		fi
	done
	rm -rf "$backup"
	echo "Removed the SSH key and restored ~/.ssh/config and ~/.ssh/known_hosts."
}

case "${1:-}" in
setup) setup ;;
cleanup) cleanup ;;
*)
	echo "usage: action-ssh.sh setup|cleanup" >&2
	exit 2
	;;
esac
