# laravel: the laravel profile

What: Laravel, from laravel.tables: users (name, email, password, remember and
two-factor tokens), sessions, tokens, cache, jobs and telescope tables
truncated, notifications scrubbed; .env set for staging and Echo.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles laravel lists every table, column and rule with what it was checked
against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

