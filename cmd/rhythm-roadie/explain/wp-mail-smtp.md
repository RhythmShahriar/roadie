# wp-mail-smtp: the wp-mail-smtp profile

What: WP Mail SMTP: the email log and debug events truncated; the mailer
switched to the PHP mailer with its credentials blanked so Echo sees every
mail.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles wp-mail-smtp lists every table, column and rule with what it was
checked against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

