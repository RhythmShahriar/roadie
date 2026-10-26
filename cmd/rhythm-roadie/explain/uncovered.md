# uncovered: the copy stopped on uncovered columns (exit 1)

What: Columns that look personal have no rule.

Why it matters: Roadie fails closed: it doesn't copy data it can't
pseudonymise.

By hand: Add them under columns: (rhythm-roadie explain uncovered-column), or
pass --allow-uncovered to copy and list them as findings.

