# import: the import failed (exit 3)

What: The target database refused the import, for example a MySQL 8 collation
on an older MariaDB, or a table the input fills that the target doesn't have.

Why it matters: Nothing is replaced when the import fails before the first
table.

By hand: Run again with --fix-collation, or fix what the message names. A copy
from a live PostgreSQL database brings rows only: create the schema on the
target first (run the app's migrations there or load pg_dump --schema-only).
