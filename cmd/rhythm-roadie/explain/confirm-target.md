# confirm-target: copy needs the target confirmed (exit 2)

What: copy ran without a terminal and without --confirm-target, the name
given to --confirm-target doesn't name the target, or the replace question
was answered no.

Why it matters: copy replaces the target's database, so a run in CI or a
script must say which target it means.

By hand: Pass --confirm-target <name> (the site's host or the database name
the message gives; for a SQLite target its absolute path or the file's name). On a terminal, answer y to the replace question or pass
--yes.
