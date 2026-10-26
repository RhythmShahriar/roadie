# wordpress: the wordpress profile

What: WordPress core: users, usermeta, comments and order notes, commentmeta,
options (admin email, siteurl, home, mailer settings) and transients.
user_pass gets an unusable hash unless the user is kept.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles wordpress lists every table, column and rule with what it was checked
against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

