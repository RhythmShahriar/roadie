# config: a problem in .roadie/config.yaml or on the command line (exit 2)

What: An unknown key, a wrong type, a bad glob, a non-ROADIE_ variable or a
non-reserved email domain in the config; or a flag or a source or target the
command can't take, such as a missing --from or a dump file given to prepare.

Why it matters: Roadie reads its config and flags strictly so a typo never
turns a rule off. Nothing was read or written; the target is untouched.

What each command takes:

- plan and copy: --from and --to each take a dump file (.sql, .sql.gz,
  .sql.zst), - for a stream, a site folder, ssh://user@host/path, a WP-CLI
  @alias or a database DSN.
- prepare: a staging site or database only (a site folder,
  ssh://user@host/path, @alias or DSN). It changes the staging in place, so
  a dump file is for copy: rhythm-roadie copy --from shop.sql --to out.sql.
- verify: a staging site, database or dump file.

By hand: Fix the line or the flag the message names; rhythm-roadie config
check validates the file.
