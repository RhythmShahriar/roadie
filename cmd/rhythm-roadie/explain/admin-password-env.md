# admin-password-env: the kept users' password set from an environment variable

What: With keep_users and --admin-password-env ROADIE_<NAME>, each kept
user's password hash becomes the hash of that variable's value, so staff log
in to the copy with a password that was never production's.

Why it matters: A kept user keeps their real login otherwise. A password
that works on staging may work on production.

What Roadie does: copy and prepare read only ROADIE_ variables and never
write the password itself anywhere.

By hand: Set the password with wp user update <login> --user_pass=<new
password> after the copy.
