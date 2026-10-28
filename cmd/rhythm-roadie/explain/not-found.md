# not-found: a path the command names doesn't exist (exit 2)

What: The dump file, site folder or SQLite database given as the source, the
target or the copy to verify isn't there.

Why it matters: Roadie stops before it reads or writes anything; the target is
untouched.

By hand: Check the path for a typo and that it is relative to the folder you
run Roadie from, or give it in full. Roadie never creates a SQLite database:
for a new SQLite target create the empty file first (touch <path>).
