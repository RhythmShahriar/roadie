# import-mixed: the import stopped half way (exit 3)

What: The import stopped after the first table was replaced: the connection
was lost or the server refused a later table.

Why it matters: Some tables are pseudonymised, the rest still hold the earlier
copy.

By hand: For copy, fix the cause and run the same command again. For prepare,
restore the backup the stop names first (the command is in the message and in
data.error.restore), then run prepare again: a new prepare backs up the mixed
database, not the one you had.
