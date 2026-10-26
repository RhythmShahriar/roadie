# wp-cli: WP-CLI isn't there (exit 3)

What: wp isn't on the remote host's PATH.

Why it matters: Remote exports and imports go through WP-CLI.

By hand: Install WP-CLI there, set remote.wp to its path, or export the
database yourself and use --from dump.sql.gz.

