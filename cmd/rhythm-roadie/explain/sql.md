# sql: the sql profile

What: Generic SQL: only what columns: lists, plus the uncovered-column scan by
name and content.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles sql lists every table, column and rule with what it was checked
against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

