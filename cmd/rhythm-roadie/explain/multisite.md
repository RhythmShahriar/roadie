# multisite: the multisite profile

What: WordPress multisite: blogs and site hosts rewritten, sitemeta (admin
email, site admins, siteurl), signups, registration_log and blogmeta, for
every wp_N_ site table.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles multisite lists every table, column and rule with what it was checked
against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

