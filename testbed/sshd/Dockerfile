# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The production host for copy --from ssh:// (r19): OpenSSH with WP-CLI
# and the MariaDB client, and the production site mounted read only. The
# host key and the authorized key are made at make up into runtime/ssh/
# and mounted in; key login only, as the user roadie.
FROM wordpress:cli-2.12.0-php8.3@sha256:661db880e953da051abcac6c0a769767b995a82cd52101c003ca7438dd427d7d
USER root
RUN apk add --no-cache openssh-server mariadb-connector-c \
	&& adduser -D -s /bin/sh roadie \
	&& sed -i 's/^roadie:!/roadie:*/' /etc/shadow
# MySQL 8.4 in the testbed has a self-signed certificate the MariaDB
# client refuses (see mysql-wrapper.sh).
COPY mysql-wrapper.sh /usr/local/bin/mysql
RUN for n in mariadb mysqldump mariadb-dump; do ln -s mysql /usr/local/bin/$n; done
COPY sshd_config /etc/ssh/sshd_config.testbed
COPY start.sh /usr/local/bin/testbed-sshd
ENTRYPOINT ["/usr/local/bin/testbed-sshd"]
