# key.authorize-net-live: a live Authorize.net API login and transaction key (critical)

What: A live Authorize.net API login and transaction key in the database,
wp-config.php, .env or, for a site target, in code.

Why it matters: A live key lets the copy act as production at the provider:
charge cards, send mail or messages, change accounts.

What Roadie does: swap (the default) replaces it with the test key set under
keys.test_keys when that key is a test key, else blanks it; blank blanks every
live key. A key in code is reported with action manual: Roadie only edits the
database, wp-config.php and .env.

By hand: Give a test key in keys.test_keys (for example
${ROADIE_AUTHORIZE_NET_TEST}) or blank the value, then run verify.

