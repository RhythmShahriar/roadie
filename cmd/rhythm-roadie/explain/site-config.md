# site-config: the site's config can't be read as text (exit 3)

What: wp-config.php or .env sets the database credentials from getenv(), $_ENV
or an expression.

Why it matters: Roadie reads config files as text and never runs PHP.

By hand: Pass the database as a DSN with ROADIE_TO_PASSWORD, or use a WP-CLI
alias.

