#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The official WordPress wp-config.php reads its database settings from
# the environment, which an SSH session doesn't inherit. Hand the session
# the container's WORDPRESS_DB_* values (from runtime/db.env) through
# sshd's SetEnv, in a file only root reads, then run sshd.
set -eu
umask 077
{
	cat /etc/ssh/sshd_config.testbed
	printf 'SetEnv WORDPRESS_DB_HOST=%s WORDPRESS_DB_USER=%s WORDPRESS_DB_NAME=%s WORDPRESS_DB_PASSWORD=%s\n' \
		"$WORDPRESS_DB_HOST" "$WORDPRESS_DB_USER" "$WORDPRESS_DB_NAME" "$WORDPRESS_DB_PASSWORD"
} > /etc/ssh/sshd_config.run
exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config.run
