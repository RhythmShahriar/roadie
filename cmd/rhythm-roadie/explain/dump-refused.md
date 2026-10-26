# dump-refused: a statement in the dump could run code (exit 3)

What: The dump holds a client command, USE of another schema, CREATE DATABASE,
LOAD DATA, INTO OUTFILE, or a trigger, procedure, function or view.

Why it matters: Dumps are code: these statements could run on the staging
database or read files.

By hand: Export with --skip-triggers, or pass --keep-routines to keep routines
without their DEFINER.

