# dialect-mismatch: source and target speak different SQL (exit 2)

What: A MySQL dump into PostgreSQL or SQLite, a PostgreSQL dump into MySQL,
or a SQLite database into MySQL or PostgreSQL: any copy between two kinds.

Why it matters: Roadie copies MySQL to MySQL or MariaDB, PostgreSQL to
PostgreSQL and SQLite to SQLite. It stops before anything is written.

By hand: Pick a target of the same kind.

