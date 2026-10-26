#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# MariaDB's client (11.8 here) verifies the server certificate by
# default and MySQL 8.4 in the testbed has a self-signed one. WP-CLI
# calls the client with --no-defaults, so a config file can't relax it:
# this wrapper, installed as mysql, mariadb, mysqldump and mariadb-dump,
# keeps TLS and skips only the verification, on the sealed network.
# --no-defaults must stay the first argument.
case $(basename "$0") in *dump) real=/usr/bin/mariadb-dump ;; *) real=/usr/bin/mariadb ;; esac
if [ "${1:-}" = "--no-defaults" ]; then
	shift
	exec "$real" --no-defaults --skip-ssl-verify-server-cert "$@"
fi
exec "$real" --skip-ssl-verify-server-cert "$@"
