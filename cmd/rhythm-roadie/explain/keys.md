# keys: live keys swapped for test keys or blanked

What: A live payment, mail or API key found in the data (a gateway's
settings, a plugin option) is swapped for the test key keys.test_keys
gives, or blanked when there is none. A key in wp-config.php or .env is
handled by the kill switch's key step and counted with these.

Why it matters: A live key on a staging copy can charge real cards, send
real mail and change real accounts.

What Roadie does: keys.mode says swap (the default) or blank. A key Roadie
can't edit (in a theme's PHP file) stops the run with key-manual.

By hand: Give each provider's test key under keys.test_keys in
.roadie/config.yaml, then run the copy again.
