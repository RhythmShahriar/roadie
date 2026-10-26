# dump-format: a dump format Roadie doesn't read (exit 2)

What: The input is a pg_dump custom or directory archive, a .wpress or
Duplicator file, a zip or tar archive or a mydumper folder.

Why it matters: Roadie reads plain SQL only and never opens archives.

By hand: Export with pg_dump -Fp, mysqldump, mariadb-dump or wp db export.

