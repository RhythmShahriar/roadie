# url.production-siteurl: the copy still calls itself production (critical)

What: siteurl, home, WP_HOME, WP_SITEURL or APP_URL is still production's URL.

Why it matters: The copy then sends visitors, cookies and logins to production
and plugins act as if they were production.

What Roadie does: copy and prepare rewrite them to --url.

By hand: Pass --url (or url: in .roadie/config.yaml) and run prepare.

