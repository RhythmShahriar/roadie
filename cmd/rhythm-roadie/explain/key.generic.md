# key.generic: a secret-looking setting (high)

What: A setting whose name says secret (DB_PASSWORD, AUTH_KEY, access_token
and the other kit rules) with a value that isn't a placeholder.

Why it matters: Credentials copied from production let the staging copy act as
production at a third party.

What Roadie does: Live keys are swapped for the test key you set in
keys.test_keys or blanked.

By hand: Blank the value, or list a known harmless value under keys.ignore
with a reason.

