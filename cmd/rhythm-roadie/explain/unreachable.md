# unreachable: a database or host doesn't answer (exit 3)

What: Roadie couldn't connect to the database of a DSN or a site's config: the
connection was refused, timed out or the host name didn't resolve.

Why it matters: Without a connection nothing can be read or written; the target
is untouched unless the message says otherwise.

By hand: Check that the database is running and reachable from this machine
(a firewall, a VPN, an SSH tunnel), then the host and port in the DSN or the
site's config.
