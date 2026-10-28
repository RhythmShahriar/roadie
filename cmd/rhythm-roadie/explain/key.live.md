# key.live: the check for live provider keys

What: The check in data.checks, the JUnit cases and the checks table that
stands for every key.<provider>-live finding (key.stripe-live,
key.aws-live, key.sendgrid-live and the rest), so the list has one line for
live keys whatever the provider. It reads every value in the database, the
defines in wp-config.php (strings inside a define built from an expression,
such as AS3CF_SETTINGS, included), .env and, for a local site, the theme and
plugin PHP files.

Why it matters: A live key lets the copy act as production at the provider:
charge cards, send mail or messages, change accounts.

What Roadie does: copy and prepare swap each live key for the test key under
keys.test_keys or blank it; verify reports any that are left, each under its
provider's ID. rhythm-roadie explain key.<provider>-live has the provider's
page, for example explain key.stripe-live.

By hand: Give a test key in keys.test_keys or blank the value, then run
verify.
