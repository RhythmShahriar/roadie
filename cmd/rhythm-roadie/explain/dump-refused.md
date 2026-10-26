# dump-refused: a statement in the dump could run code (exit 3)

What: The dump holds a statement Roadie never copies: a client command, USE of
another database, CREATE DATABASE, LOAD DATA, INTO OUTFILE, a trigger,
procedure, function or view (without --keep-routines) or a statement that is on
no list below. The message names the line and the kind, never its data.

Why it matters: Dumps are code: these statements could run on the staging
database, read or write files on its server or reach another server.

Kept (MySQL and MariaDB): CREATE TABLE with a known engine, CREATE INDEX,
CREATE SEQUENCE, DROP TABLE or DROP INDEX of an object the dump creates,
LOCK and UNLOCK TABLES, ALTER TABLE that adds keys or changes columns and
table options, transactions, DELIMITER lines, SET NAMES and the session
settings dump tools write (character sets, time_zone, sql_mode,
foreign_key_checks, unique_checks, autocommit and user variables).

Kept (PostgreSQL): the SET and set_config() settings pg_dump writes, CREATE
TABLE, INDEX, SEQUENCE, TYPE, DOMAIN and SCHEMA, the common extensions
(pgcrypto, uuid-ossp, citext, pg_trgm, hstore, postgis, vector and others),
ALTER TABLE without SET TABLESPACE, COMMENT ON, COPY ... FROM stdin,
transactions, setval() with literals and the \restrict lines of newer pg_dump.

Dropped with a plan line: GRANT, REVOKE, users and roles, SET PASSWORD,
events, ALTER DATABASE, USE of the dump's own database, GTID and
SQL_LOG_BIN settings, Percona's rocksdb_bulk_load check, ALTER ... OWNER TO,
SET SESSION AUTHORIZATION and publications.

Refused: client and psql commands (\!, \connect, source), USE of another
database, CREATE or DROP DATABASE, LOAD DATA, INTO OUTFILE, LOAD_FILE(), COPY
... PROGRAM or to a file, DO blocks, CALL, UPDATE, DELETE, INSERT ... SELECT,
DROP ... CASCADE, SET GLOBAL, a SET of another variable, a SET of the client
character set to a multibyte one such as SJIS, BIG5 or GBK, a SET that mixes
SQL_LOG_BIN with other settings, a change of standard_conforming_strings or
sql_mode Roadie can't follow, a call to set_config() of another setting, the
server admin and file functions (pg_read_file(), lo_import(), dblink), names or
strings with Unicode escapes (U&), CREATE SCHEMA that creates objects inside
it, table names that carry their database (db.t), FEDERATED or CONNECT tables,
foreign servers and subscriptions. With --keep-routines triggers, procedures,
functions and views are kept with DEFINER=CURRENT_USER; SONAME functions and
untrusted languages are refused even then.

By hand: Export with --skip-triggers and without --routines, one database at a
time (no --databases or --all-databases), with --default-character-set=utf8mb4
or pg_dump -E UTF8, or pass --keep-routines to keep routines without their
DEFINER. Leave anything else the message names out of the export.
