# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# WordPress 6.8.3 for prod-wp and staging-wp, with WP-CLI and the plugins
# the scenarios need, at the versions testbed/Makefile pins. Every zip is
# checked against its sha256 before it is unpacked; a mismatch fails the
# build. This is the only step of the testbed that downloads anything, and
# it runs at image build time, never while a scenario runs.
FROM wordpress:cli-2.12.0-php8.3@sha256:661db880e953da051abcac6c0a769767b995a82cd52101c003ca7438dd427d7d AS cli
FROM wordpress:6.8.3-php8.3-apache@sha256:30bff39330d1693b0ce13d32fc9b7bb67193064f040b7d60d3494e136fa599d4
COPY --from=cli /usr/local/bin/wp /usr/local/bin/wp

# "<url> <sha256> <directory name>" per line.
ARG PLUGINS
COPY install-plugins.php /tmp/install-plugins.php
RUN php /tmp/install-plugins.php /usr/src/wordpress/wp-content/plugins "$PLUGINS" \
	&& rm /tmp/install-plugins.php \
	&& chown -R www-data:www-data /usr/src/wordpress/wp-content/plugins

# The loader that runs WooCommerce Subscriptions core as a plugin.
COPY plugins/ /usr/src/wordpress/wp-content/plugins/
COPY seed.sh /usr/local/bin/testbed-seed
