# unreachable: a database or host doesn't answer (exit 3)

What: Roadie couldn't connect to the database of a DSN or a site's config: the
connection was refused, timed out or the host name didn't resolve.

Why it matters: Without a connection nothing can be read or written; the target
is untouched unless the message says otherwise.

By hand: Check that the database is running and reachable from this machine
(a firewall, a VPN, an SSH tunnel), then the host and port in the DSN or the
site's config (DB_HOST in wp-config.php or .env).

A server that answers but refuses the connection (an unknown database, a wrong
password, a user without rights) stops with this code too: check the database
name, the user and its password.
