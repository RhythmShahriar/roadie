# import: the import failed (exit 3)

What: The target database refused the import, for example a MySQL 8 collation
on an older MariaDB.

Why it matters: Nothing is replaced when the import fails before the first
table.

By hand: Run again with --fix-collation, or fix what the message names.

