# pii.name: a real-looking name in a name column (medium)

What: A value in a name column (first_name, last_name, display_name, author
and similar) that isn't from Roadie's fake name lists.

Why it matters: Names identify people, more so next to an address or an order
history.

What Roadie does: Names in covered columns get fakes from the locale lists;
names in free text are replaced when Roadie saw them in a structured column
first.

By hand: Add the column under columns: with rule: first_name, last_name or
full_name.

