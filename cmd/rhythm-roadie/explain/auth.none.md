# auth.none: the copy answers without a password (low)

What: With --site the staging home page answered without authentication.

Why it matters: noindex is only a request; basic auth keeps people and
crawlers out.

What Roadie does: --basic-auth <user> writes snippets for Apache, nginx and
Caddy with a generated password.

By hand: Add basic auth in the web server for the staging host.

