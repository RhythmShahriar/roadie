# dump-format: a dump format Roadie doesn't read (exit 2)

What: The input is a pg_dump custom or directory archive, a .wpress or
Duplicator file, a zip or tar archive, a mydumper folder or a dump saved as
UTF-16 (PowerShell's > redirect writes that). A UTF-8 dump with a byte order
mark is fine.

Why it matters: Roadie reads plain SQL in UTF-8 only and never opens archives.

By hand: Export with pg_dump -Fp, mysqldump, mariadb-dump or wp db export. On
Windows write the dump with mysqldump --result-file or pg_dump -f, not with >.

