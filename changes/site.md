# Changes: lane site

User-visible behaviour of `site/` (endpoints, remote commands, sites, guards, the lock, the kill switch and the exposure guard). Messages are exact. Docs and wiki pages are written from this file.

## 1. Endpoint grammar (`site.ParseEndpoint`)

Shapes are tried in this order:

| Input | Kind | Fields |
|---|---|---|
| `-` | `stdio` | Display `-` |
| `@name` (letters, digits, `.`, `_`, `-`) | `wp-alias` | Alias `@name` |
| `mysql://`, `mariadb://`, `postgres://`, `postgresql://` | `dsn` | Driver, User, Host, Port, Path = database name, DSN without the password |
| `sqlite:///abs/path.sqlite` (also `sqlite:///C:/x.sqlite`) | `dsn` | Driver `sqlite`, Path = the file |
| `ssh://[user@]host[:port]/path` | `ssh-site` | User, Host, Port, Path |
| `[user@]host:/path` (scp-like, needs the `@`) | `ssh-site` | as above; an existing local file or folder of that name wins |
| a path ending `.sql`, `.sql.gz`, `.sql.zst`, `.sql.bz2`, `.dump` (any case) | `dump-file` | Compression `gzip`, `zstd`, `bzip2` or `""` from the extension |
| an existing regular file | `dump-file` | |
| an existing folder | `local-site` | |

Windows paths (`C:\x`, `C:/x`, `\\server\share`) are always local, never scp-like ssh.

Passwords: a DSN password (user info or a `password`, `pass`, `pwd` or `sslpassword` query parameter) moves into `Endpoint.Password`. `DSN` holds the value without it. `Display` shows `•••` in its place, e.g. `mysql://shop:•••@db.example/shop`. The caller fills `Password` from `ROADIE_FROM_PASSWORD` / `ROADIE_TO_PASSWORD` when it is empty.

New exported helpers (added, nothing frozen changed):

- `site.InlinePassword(e core.Endpoint) bool`: the DSN carried its own password. Call before filling from the environment.
- `site.InlinePasswordWarning(flag string) string`: `the password in --from is visible to other users of this machine; use ROADIE_FROM_PASSWORD` (and the `--to` / `ROADIE_TO_PASSWORD` form).

Errors are kit `cli.UsageError` (exit 2), exact texts:

- `endpoint: <value> is not a dump file, site folder, ssh://, @alias or DSN` (a password in a URL-shaped value is shown as `•••`)
- `endpoint: give a dump file, site folder, ssh://, @alias or DSN` (empty value)
- `endpoint: ssh host "<host>" is refused: a host can't start with '-'`
- `endpoint: ssh host "<host>" is refused: a host can't hold whitespace or control characters`
- `endpoint: ssh host "<host>" is refused: a host can't hold "<char>"`
- `endpoint: ssh user "<user>" is refused: ...` (same reasons)
- `endpoint: <value> holds a password; Roadie uses your ssh keys and agent, never an ssh password`
- `endpoint: <value> needs the site's folder on the host, such as ssh://deploy@shop.com/var/www/shop`
- `endpoint: <value> has a bad port`
- `endpoint: <value>: an ssh:// address takes no ?query or #fragment`
- `endpoint: the remote path in "<value>" holds a control character`
- `endpoint: <value>: give the absolute folder on the host, such as /home/deploy/www; Roadie doesn't expand '~'` (an scp-like path starting `~`, or an `ssh://host/~...` path: every remote command single-quotes the folder, so `~` would never expand)
- `endpoint: @ needs an alias name from wp-cli.yml, such as @staging`
- `endpoint: "<value>" is not a WP-CLI alias name (letters, digits, '.', '_' and '-')`
- `endpoint: <value>: a SQLite DSN is sqlite:///absolute/path.sqlite`
- `endpoint: <value> has no host`
- `endpoint: <value> is not a valid <scheme> DSN`
- `endpoint: <value> can't be read: <cause>` (stat failed for a reason other than "not there")

Hosts pass only letters, digits, `.`, `_`, `-` (plus `:` and `%` inside `[...]` for IPv6). Fuzz target `site:FuzzEndpoint` (never panics; Display and DSN are the same whatever the password).

## 2. wp-config.php reader and editor (`site/wpconfig`)

Reads with a PHP tokenizer (no PHP ever runs): open and close tags, `//`, `#` and `/* */` comments, single and double quoted strings with escapes, heredoc and nowdoc, backticks plus nesting (blocks, brace-less `if`/`else`/loops, `if (): ... endif;`). A define inside a comment, string or nowdoc is ignored. `$o->define()`, `Foo::define()` are not the built-in; `\define()` is.

- `Pairs()`: every define with a literal value (string, `true`/`false`, number) in file order, conditional ones included.
- `Get(name)`: the define PHP keeps (first unconditional, else first conditional) when literal.
- `Unresolved()`: names whose kept define is built from `getenv()`, `$_ENV`, `$_SERVER`, a constant or any expression (the standard `ABSPATH` define is listed).
- `TablePrefix()`: `$table_prefix = '...';` at the top level, else `""`.
- `DB()`: `DB_NAME`, `DB_USER`, `DB_PASSWORD`, `DB_HOST` when all are literals.
- Added: `Lookup(name) (Define, bool)`, `Defines() []Define` (Name, Value, Literal, Quoted, Conditional, BeforeSettings, Line), `SetBool(name, bool)`, `Quote(v, q) string` (the one PHP quoting function).

Edit rules for `Set(name, value)` / `SetBool`:

1. The first unconditional define before `require_once ... wp-settings.php` is changed in place: only its value bytes change; quote style (`'` or `"`) and spacing are kept. Any conditional define of the same name before it gets the same value, so no branch keeps production.
2. With none, `define( 'NAME', 'value' );` goes on its own line before the line of the `/* That's all, stop editing! ... */` comment (straight or curly apostrophe; the "between this line and the stop editing line" comment doesn't count); else before the `require_once` of `wp-settings.php`; else before a final `?>`; else at the end. Never after `wp-settings.php`. When a conditional define of the name comes earlier, the new one goes before that statement.
3. A define found only after `wp-settings.php` is too late: a new one is inserted before it (the late one stays; PHP warns about it).
4. Values: single quoted (`\` and `'` escaped); double quoted when the old define used `"` or the value holds a control character (`\n`, `\t`, `\xHH`, `$` and `"` escaped).
5. BOM, CRLF or lone-CR line ends and the file's trailing newline (or its absence) are kept; inserted lines use the file's line ending. `Bytes()` is byte-identical with no edit; a second identical `Set` changes nothing.
6. Refused (the file stays untouched) with: `wp-config.php: found no place before wp-settings.php to add NAME; add define( 'NAME', ... ); there by hand`, `wp-config.php: NAME can't be set safely in this file; set it by hand before wp-settings.php`, `wp-config.php: "<name>" is not a PHP constant name`. `Parse` fails only on a comment, string or heredoc that never ends (`wp-config.php: unreadable PHP: a /* comment at line N never ends`).

Every edited fixture passes `php -l`. Fuzz target `site/wpconfig:FuzzWPConfigEdit`.

## 3. .env reader and editor (`site/dotenv`)

Reads `KEY=value`, `export KEY=value`, `KEY = value`, single quoted (no escapes, may span lines), double quoted (escapes `\" \\ \$ \n \r \t \f \v`, may span lines), unquoted up to a `#` that follows whitespace, trailing blanks trimmed. `${OTHER}` kept as written. Comments, blank lines and anything that isn't an assignment are kept byte for byte.

- `Pairs()`: every assignment in file order, duplicates included. `Get`: the last one (Laravel). `Unresolved()`: names whose last value holds `${...}` (unquoted or double quoted).
- `Set`: replaces the last occurrence in place (a `# comment` after it goes when the value changes because it described the old value; it stays when the value is the same); a new key is appended as `KEY=value` plus the file's line ending, or after a line ending when the file had no trailing one.
- Quoting (exported `dotenv.Quote`): unquoted when the value holds only `A-Z a-z 0-9 _ . / : @ + -` (so `base64:...=` values get quoted), else double quoted with the escapes above. Other control characters are refused: `.env: NAME: the value holds control character 0x.., which a .env file can't carry`.
- Refused: `.env: "<name>" is not a variable name`, `.env: NAME can't be set safely in this file; set it by hand`; `Parse` fails on a quoted value that never ends (`.env: unreadable: the quoted value of NAME at line N never ends`).

Fuzz target `site/dotenv:FuzzDotenv`.

## 4. Remote transport (`site/remote`)

Every ssh call: `ssh [-p <port>] [-o BatchMode=yes] -- [user@]host '<command>'`. BatchMode is added with `site.Options.BatchMode` or when stdin is not a terminal. `StrictHostKeyChecking` and `UserKnownHostsFile` are never passed. Every argument goes through one POSIX single-quote function (`remote.Quote`). The remote command string of every call is recorded (`Runner.Commands()`, for `Site.RemoteCommands`).

The allowlist as built (`remote.List(true)` prints it for plan; `<wp>` is `wp` or `remote.wp`, always with `--path='<site path>'`):

Source (read-only):

- `wp --path=<path> core version --skip-plugins --skip-themes`
- `wp --path=<path> option get siteurl|home --skip-plugins --skip-themes`
- `wp --path=<path> db export - --skip-plugins --skip-themes --single-transaction --skip-triggers`
- `wp --path=<path> db export - --tables=<list> --skip-plugins --skip-themes --single-transaction --skip-triggers` (options-only Source and `--only`)
- `cat -- <path>` and `test -f -- <path>`: on a source only for `wp-config.php` and `.env`
- `find <path> -maxdepth <n> -printf '%y %s %P\n'` (GNU find; on a host whose find has no `-printf`, such as BSD, macOS or busybox, List and Exists fail with `ssh: find on <user@host> has no -printf, so it isn't GNU find. Roadie lists remote files with GNU find; install findutils there.`, exit 3)
- `mysqldump --defaults-extra-file=/dev/stdin --single-transaction --skip-triggers --no-tablespaces --hex-blob <db> [<table> ...]`
- `IFS= read -r PGPASSWORD; export PGPASSWORD; exec pg_dump --format=plain --no-owner --no-privileges --host=<h> --port=<n> --username=<u> --dbname=<db> [<table> ...]`

Target, additionally:

- `wp --path=<path> db import -`
- mysql through a wrapper: `umask 077 && f=$(mktemp) && trap 'rm -f "$f"' EXIT && while IFS= read -r l && [ "$l" != '#roadie-end' ]; do printf '%s\n' "$l"; done > "$f" && mysql --defaults-extra-file="$f" <db>`. PRD 6.13's `mysql --defaults-extra-file=/dev/stdin` can't work: mysql would read the whole stdin (the dump) as its option file. The credentials still travel only on stdin.
- `IFS= read -r PGPASSWORD; export PGPASSWORD; exec psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 --host=<h> --port=<n> --username=<u> --dbname=<db>`
- backup: `set -C && umask 077 && mkdir -p ~/.roadie-backups && { wp --path=<path> db export - --skip-plugins --skip-themes --single-transaction || echo 'roadie: wp db export failed' >&2; } | gzip > ~/.roadie-backups/<UTC 2006-01-02T15-04-05>.sql.gz` (the echo marks a failed export, since POSIX sh loses wp's exit status in the pipe; `set -C` means a backup another run made on that host in the same second is never overwritten: `backup: ~/.roadie-backups/<ts>.sql.gz already exists on <user@host>: another backup started there in the same second. Run again`)
- write: `umask 077 && cat > <tmp> && mv -f -- <tmp> <path>`, or for a new file with the caller's mode: `umask 077 && cat > <tmp> && chmod <0nnn> -- <tmp> && mv -f -- <tmp> <path>`
- `ls -lnLd -- <path>` (reads an existing file's mode, owner and group before a write)
- write over an existing file, keeping its mode and, where the ssh user may set them, its owner and group: `umask 077 && cat > <tmp> && { chown <uid>:<gid> -- <tmp> 2>/dev/null || chgrp <gid> -- <tmp> 2>/dev/null || :; } && chmod <0nnn> -- <tmp> && mv -f -- <tmp> <path>`
- `mkdir -p -- <path>`, `rm -f -- <path>`
- `wp --path=<path> eval-file - --skip-plugins --skip-themes` (the Echo connector script on stdin; plugins and themes stay unloaded, so a copied plugin can't send mail before the connector is in place)
- `cat -- <path>`, `test -f -- <path>` for any path (the kill-switch checks read the mu-plugins)

A template outside the list panics under `go test` and is an error otherwise (`remote command not in the allowlist`). Arguments with NUL or line breaks are refused.

Credentials: mysqldump/mysql get `[client]` with `user=`, `password=`, `host=` (each in whichever quote it doesn't hold, backslashes doubled), `port=` and a `#roadie-end` line on stdin; pg_dump/psql get the password as the first stdin line. A password with a line break, or with both quote kinds for MySQL, is refused: `credential can't be sent on stdin: ...`.

Errors (`*core.Error`, exit 3):

- `ssh-host-key: deploy@shop.com isn't in known_hosts (ED25519 SHA256:…). Connect once with ssh to check and add it; Roadie never skips host key checks.` (from `Host key verification failed` or `No ED25519 host key is known`; the fingerprint part only when ssh printed it; `has a host key that changed since known_hosts recorded it` for a changed key)
- `ssh: deploy@shop.com refused the key (Permission denied (publickey)). Check ssh deploy@shop.com works on its own; Roadie uses your ssh setup and never skips host key checks.`
- `wp-cli: wp isn't on PATH for deploy@shop.com. Install WP-CLI there, set remote.wp in .roadie/config.yaml to its path, or export the database yourself and use --from dump.sql.gz.` (`wp` is replaced by `remote.wp` when set)
- other ssh failures (exit 255): `ssh: couldn't run a command on deploy@shop.com (<ssh's own message>). Check ssh deploy@shop.com works on its own; Roadie uses your ssh setup and never skips host key checks.`
- a command that ran and failed is `*remote.RunError` (template, exit code, stderr tail) so the site code maps `ERROR 1273` and the like.

WP-CLI aliases (`remote.ResolveAlias(dir, "@prod")`): `wp-cli.yml` and `wp-cli.local.yml` (local wins) in the first folder from the working folder up that has either. Forms `ssh: deploy@shop.com/var/www/shop`, `ssh: user@host:2222/path`, `ssh: host:/path`, with an optional `path:` that overrides; an alias with only `path:` is a `local-site` (relative to the file's folder). Refused: alias groups (`@all: [@a, @b]`), docker/vagrant or other schemes, bad hosts, no site folder. Messages: `@prod is in no wp-cli.yml or wp-cli.local.yml from <dir> up`, `@prod in wp-cli.yml is an alias group or not a mapping; give one site`, `@prod: ssh: "<v>" is not user@host[:port]/path (Roadie runs only ssh aliases)`, `@prod: ssh: "<v>" names no site folder (add /path, or a path: key)`. The host never holds `~` (as in WP-CLI, `deploy@shop.com~/www` is a folder in the ssh user's home) and an ssh alias whose site folder is relative or starts with `~` or `/~` is refused before anything runs: `@prod: give the absolute folder on the host, such as /home/deploy/www, not "~/www"; Roadie doesn't expand '~'`; a folder with a control character: `@prod: the site folder "<path>" holds a control character`. Fuzz target `site/remote:FuzzAliasSSH`.

Tests: an ssh shim (`site/remote/testdata/sshshim`, built by `site/remote/remotetest.Install` in TestMain) records argv and stdin and replays canned output; `site/remote/execgrep_test.go` fails when `exec.Command("ssh"` (or `CommandContext`) appears outside `site/remote/` or `exec.Command("wp"`/`("php"` in non-test code anywhere in the module.

## 5. Sites (`site.Open`, `site.DetectStack`)

`Open` takes `local-site`, `ssh-site`, `wp-alias` and `dsn`; a `dump-file` or `stdio` endpoint is an error (`site: <display> is a <kind>: not a site endpoint`).

Added to `site.Options` (nothing frozen changed): `FixCollation bool` (passed to every import the site runs) and `BackupDir string` (`""` = `.roadie/backups` in the working folder). Added exported `site.TableCounter` (`ExpectTables(n int)`), implemented by ssh and alias sinks.

`DetectStack`: `wordpress` when `wp-config.php` or `wp-includes/version.php` exists; `laravel` when `artisan` and `.env` both exist; else `sql`. A DSN is always `sql`. `List` never reads file contents.

Read-only sources refuse `WriteFile`, `Remove`, `Sink`, `Backup` and every WP call other than `core version` / `option get siteurl|home` with `core.ErrReadOnly` (local, ssh, alias and DSN alike).

Local sites:

- Every file call goes through an `os.Root`, so neither `../x` nor a symlink inside the site reaches outside it (`site: "<rel>" is outside the site folder`).
- `WriteFile`: temp file `.<name>.roadie-<12 hex>` in the same folder, fsync, chmod, rename. An existing file keeps its mode (and its owner when Roadie runs as root); a new file gets the given perm; new folders 0755. Windows: the rename is retried 10 times over 2 s, then `site: couldn't replace <rel>: another program holds it open (<cause>). Close it and run again`.
- `WP` always returns `core.ErrNoWPCLI`. Deviation from the lane text ("WP runs when wp is on PATH"): running `wp` on local files loads the site's PHP, which PRD 18.1 forbids and which the execgrep test (item 4) refuses. Callers fall back to reading options from the database.
- DB credentials: WordPress from `wp-config.php` (`DB_NAME`, `DB_USER`, `DB_PASSWORD`, `DB_HOST`; `DB_HOST` read as `host`, `host:port`, `host:/socket`, `:/socket`, `[v6]:port`); Laravel from `.env` (`DB_CONNECTION` mysql, mariadb, pgsql, postgres or sqlite; `DB_HOST` default `127.0.0.1`, `DB_PORT` default by driver, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`; SQLite default `database/database.sqlite`, relative to the site). The DSN given to `db.Open` is `mysql://user@host:port/db` (plus `?socket=<path>`), `postgres://...`, `sqlite:///abs/path`; the password only in `Endpoint.Password`.
- `site-config` errors (exit 3), exact text:
  - `site-config: <dir>/wp-config.php sets DB_PASSWORD from getenv('DB_PASS'), which Roadie can't run. Pass the target as a DSN with ROADIE_TO_PASSWORD, or use @staging through WP-CLI.` (on a source: `the source`, `ROADIE_FROM_PASSWORD`, `@prod`). The expression is quoted only when it is one plain lookup with a literal name: `getenv('X')`, `env('X')`, `$_ENV['X']` or `$_SERVER['X']` (names of letters, digits and `_`). Anything else, including the fallback forms `getenv('X') ?: '<literal>'` and `$_ENV['X'] ?? '<literal>'` and concatenations, is shown as `a PHP expression`, so a secret written in PHP is never echoed. The same rule applies to the `WP_ENVIRONMENT_TYPE` guard message.
  - `.env` `${OTHER}` reference: `... sets DB_PASSWORD from another variable (${...}), which Roadie can't run. ...`
  - `site-config: <file> doesn't set <NAME>. Add it there, or pass the target as a DSN with ROADIE_TO_PASSWORD.`
  - `site-config: <file> sets DB_CONNECTION=<v>; Roadie reads mysql, mariadb, pgsql and sqlite. Pass the database as a DSN instead.`
  - `site-config: <file> has a bad DB_PORT. Fix it, or pass the database as a DSN.`
  - `site-config: <dir> has no wp-config.php or Laravel .env to read the database from. Pass the database as a DSN instead.`
  - `site-config: <file> can't be read: <parse error>`
- `DBIdentity`: `{Engine, Server: "", Host (lower case; "localhost" for a socket), Port (default by driver), Name, User, Prefix ($table_prefix)}`; for SQLite `Name` is the absolute file.

SSH and alias sites (all through the item 4 allowlist):

- `ReadFile` = `cat -- <path>` (missing file = `fs.ErrNotExist`); `WriteFile` = `mkdir -p -- <dir>`, `ls -lnLd -- <path>`, then for a new file the write-with-mode template with the caller's mode, or for an existing one the write-keep template with the mode, uid and gid ls printed (a deploy-owned 0644 wp-config.php stays 0644, so PHP-FPM running as another user still reads it; set-id and sticky bits are dropped; a non-root ssh user usually keeps the group but not the owner). ls output Roadie can't read, a non-regular file or an ls failure other than "No such file or directory" stops the write before anything is sent. Temp `.<name>.roadie-<12 hex>`; `Remove` = `rm -f`; `Exists` = `test -f` where allowed (target, or `wp-config.php`/`.env` on a source), else `find <path> -maxdepth 0` (also finds folders); `List` = `find <path> -maxdepth <n>` (`n<=0` = 65535).
- `WP(args)` takes only `core version` and `option get siteurl|home`; anything else is `remote command not in the allowlist: wp ...`.
- Added method `EvalFile(ctx, script)` on ssh sites (pipes into `wp eval-file -`), used by the kill switch.
- `Source`: WordPress `wp db export -` (or `--tables=a,b` for a table list); Laravel `mysqldump` / `pg_dump` with credentials on stdin from the remote `.env`. Streamed straight into `dump.Open`; the export's own error (ssh, wp-cli, host key) wins over the reader's. SQLite over ssh: `site: <display>: Roadie can't export a sqlite database over ssh; copy the file and use it as a sqlite:/// DSN`.
- `Sink`: the dump writer (`dump.NewStreamSink`) streams into `wp db import -` (WordPress) or the mysql / psql wrappers (Laravel). `DBIdentity.Server` is the ssh host, so loopback databases on two hosts are never the same.
- Import errors (exit 3), never quoting the client's stderr (a MySQL error can echo a row value; only `ERROR <n>` is kept):
  - `import: the database on <dest> doesn't know collation <c>. Run again with --fix-collation. The target is untouched.` (`import`, State untouched; from `ERROR 1273`, `Unknown collation` or PostgreSQL's `collation "x" for encoding`). Only when at most one CREATE TABLE and no DROP TABLE was written: a dump that drops each table before creating it has already removed one.
  - otherwise: `import: the database on <dest> doesn't know collation <c>, after <k> of <n> tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run again with --fix-collation.` (`import`, State mixed)
  - `import: wp db import on <dest> failed (MySQL ERROR 1045) before any table was replaced. The target is untouched.` (`import`; `(exit <n>)` when there's no MySQL code); an ssh or host key error before any table keeps its own code with State untouched
  - `import: wp db import on <dest> failed (MySQL ERROR 1062) after 37 of 61 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again.` and `import: the connection to <dest> was lost after 37 of 61 tables. ...` (`import-mixed`, State mixed; `after 37 tables` without `ExpectTables`). `k` counts CREATE TABLE statements written. A failure after a DROP TABLE but before any CREATE is mixed too (`after 0 of 61 tables`), never untouched.
- WP-CLI aliases resolve from the working folder up (item 4); the site keeps the alias endpoint with Host, User, Port and Path filled in; an alias with only `path:` opens as a local site.

A `find` line naming `..`, `../x`, an absolute path or anything that climbs out of the listed folder is dropped; names are cleaned (`a/./b` lists as `a/b`). So a broken or hostile host can't make Roadie list or read outside the site folder.

DSN sites: no files (`ReadFile`/`WriteFile` give `fs.ErrNotExist`, `Exists` false, `List` empty), stack `sql`, `DBIdentity` from the DSN itself (no connection).

Backup (`Site.Backup`):

- Local and DSN: `<BackupDir or ./.roadie/backups>/<UTC 2006-01-02T15-04-05>.sql.gz`, folder 0700 (chmod even when it existed), file 0600 by `dump.NewFileSink`. The name is reserved with O_EXCL first, so two backups started in the same second from one folder never share a file: the second gets `-2`, then `-3` and so on (`.roadie/backups/2026-10-26T08-02-03-2.sql.gz`); after 100: `backup: <dir> already holds 100 backups named <ts>; remove old ones and run again`. A failed backup removes its reservation. Restore lines:
  - WordPress: `gunzip -c <path> | wp --path=<site dir> db import -` (deviation: `--path` added so it works from any folder)
  - Laravel and DSN: `gunzip -c <path> | mysql -h <host> -P <port> -u <user> -p <db>`, `gunzip -c <path> | psql -h <host> -p <port> -U <user> -d <db>`, `gunzip -c <path> | sqlite3 <file>` (no password; `-p` asks)
- SSH and alias (WordPress only): runs the allowlisted backup command and returns path `~/.roadie-backups/<ts>.sql.gz` and restore `ssh [-p <port> ]<user@host> 'gunzip -c ~/.roadie-backups/<ts>.sql.gz | wp --path=<path> db import -'` (`remote.wp` replaces `wp`). A Laravel ssh target: `backup: Roadie backs up an ssh target only through WP-CLI; export <display>'s database yourself, then pass --no-backup`.

New fuzz targets: `site:FuzzParseFind`, `site:FuzzSplitWPHost`.

## 6. Target guards and the run lock

Added to `site.GuardInput`: `URL string` (`--url`) and `SiteURL string` (the target's siteurl when the caller already read it; else Guard asks WP-CLI over ssh or reads the options table). `Mode` is `"copy"`, `"prepare"` or `"plan"`.

Order: a stdio target has no guard; a dump-file target gets only `output-is-input`; otherwise `target-is-source` (not on prepare, where source and target are the staging), `target-is-production`, `target-says-production`, then `target-holds-production` (prepare only). Nothing is written before all pass; State is never set.

- `output-is-input` (exit 2): `output-is-input: --to <display> is the file Roadie reads. Write the copy to a new file.` (`os.SameFile`, so hard links and `./` spellings count)
- `target-is-source` (exit 3):
  - `target-is-source: --from and --to are the same folder (<path>). A copy can't replace its own source.`
  - `target-is-source: --from and --to are both <path> on <host>. A copy can't replace its own source.` (host case-insensitive, port 0 = 22, path cleaned, user ignored, aliases resolved)
  - `target-is-source: --from and --to both reach database <name> on <host>. A copy can't replace its own source.` (MySQL and MariaDB are one family; same port and name; loopback hosts only on the same machine; `<host> on <server>` for a remote loopback; `on this machine` for SQLite)
- `target-is-production` (exit 3): `target-is-production: <display> is <host>, which production: lists. Roadie never writes to production. Point --to at the staging copy.` and `... <display> uses database host <host>, which ...`. Hosts checked: `--url`, the ssh host, `WP_HOME`/`WP_SITEURL` (literal), `APP_URL`, siteurl (or home), a non-loopback DB host (from the site or the DSN). Globs (`*.shop.com`) through `urlrw.Hosts`, one entry at a time (an entry it can't read, such as a bare IPv6 address, is read by the guard itself). A host matches however it is spelled: upper case, trailing dots, a port, an IDN in Unicode or punycode, fullwidth letters and ideographic dots (NFKC), an IPv4-mapped IPv6 address or a long IPv6 form all match. A database target named by a DSN (StackSQL) has its siteurl and home read from every `*options` table with `option_name` and `option_value` (any prefix) and its network hosts from `<prefix>blogs` and `<prefix>site`: an ssh tunnel to production's database has a loopback host and no wp-config.php, so before this a copy to it passed. A DSN database that can't be reached stops with `unreachable` (exit 3). On prepare the target's own siteurl doesn't count (it is what prepare fixes).
- `target-says-production` (exit 3), override only by `--target-is-staging <host>` equal (case-insensitive) to one of the target's own names (`--url`, ssh host, WP_HOME/WP_SITEURL, APP_URL, siteurl), never for the two refusals before it:
  - `target-says-production: <display> has WP_ENVIRONMENT_TYPE unset, which WordPress reads as production. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging <host>.`
  - `... has WP_ENVIRONMENT_TYPE set to production. If it's staging, ...`
  - `... sets WP_ENVIRONMENT_TYPE from getenv('WP_ENV'), which Roadie can't run, so it counts as production. If it's staging, ...`
  - `... has APP_ENV=production in .env. If it's staging, set APP_ENV=staging there, or pass --target-is-staging <host>.`
  - `... has a wp-config.php Roadie can't read (permission denied), so it counts as production. If it's staging, make it readable, or pass --target-is-staging <host>.` (also `(the read failed)` for any other read error except not found; the remote stderr is never echoed) and the same for `.env`.
  - `... has a wp-config.php Roadie can't parse, so it counts as production. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass ...` and `... has a .env Roadie can't parse, so it counts as production. If it's staging, set APP_ENV=staging there, or pass ...`.
  - `<host>` is the first known name, else the literal `<host>`. `local` and `development` pass. A target with no wp-config.php or .env (a fresh folder) passes.
- `target-holds-production` (exit 3, prepare only): `target-holds-production: <display> still calls itself <siteurl>. Pass --production-url <siteurl> so Roadie knows which URL is live.` when neither `--production-url` nor `production:` is set and siteurl isn't `--url`. Interpretation of the lane text: a known production URL is the override (PRD 10.4), so siteurl equal to `--production-url` is not refused.

Lock (`site.Lock`, added `site.LockNotify(target, note func(string))`):

- File `<config.CacheDir("roadie")>/locks/<sha256 hex of the identity>.lock`, folder 0700, file 0600, created with O_EXCL, holding `pid=`, `started=` (RFC 3339), `target=` and `token=` lines.
- Identity: `file:<absolute path, symlinks resolved>` for local sites, dump files and SQLite; `ssh:<host>:<port>:<path>` for ssh and aliases (resolved); `db:<mysql|postgres>://<host>:<port>/<db>` for DSNs. Stdio: no lock.
- Held (exit 3): `lock: another Roadie run is copying to <display> (pid 4120, started 14:02). Wait for it, or remove <lock path> if it crashed.` (`2006-01-02 15:04` when not today).
- Stale (pid not running: Unix `kill 0`, Windows `OpenProcess`) is replaced with the note `note: removed a stale lock on <display> from pid <n> (started <time>), which is no longer running.` (`Lock` prints it on stderr). An empty lock file younger than 10 s counts as held. The stale check and removal happen while holding `<lock>.break` (O_EXCL; one older than 10 s is itself stale), so two runs that find the same stale lock never both win. When another run holds the break file for over 2 s (exit 3, `lock`): `lock: another Roadie run is clearing a stale lock in <cache>/roadie/locks. Run again.` Unlock removes only its own lock (token match).

## 7. WordPress kill switch (`site/killswitch`)

`Steps(wordpress, o)`, in order, all phase `files` (Apply of the check-only ones does nothing; their changes are DataRules):

| Step ID | Apply | Check (findings) | Undo text |
|---|---|---|---|
| `config.environment` | `define( 'WP_ENVIRONMENT_TYPE', 'staging' );` | critical `config.environment` unless `staging`/`local`/`development`, literal and before wp-settings | `set WP_ENVIRONMENT_TYPE in wp-config.php` |
| `side-effect.cron` | `define( 'DISABLE_WP_CRON', true );` | high unless true | `remove DISABLE_WP_CRON from wp-config.php` |
| `side-effect.subscriptions` | `WP_HOME`/`WP_SITEURL` (literal, when defined) to `--url` without trailing slash | critical when Subscriptions is present (option `wc_subscriptions_siteurl` or `wp-content/plugins/woocommerce-subscriptions`) and the mu-plugin filter is missing, or `wc_subscriptions_siteurl` (marker removed) is this copy's URL, or a URL define isn't the staging URL; `not-present` otherwise | `delete wp-content/mu-plugins/roadie-staging.php` |
| `key.generic` | live key defines in wp-config.php swapped or blanked (Options.Keys/Replace, default keys.ScanPairs/keys.Replace with `keys.mode`, default `swap`) | `key.<provider>-live` per live define left, masked example | |
| `config.salts` | the eight salts (AUTH_KEY .. NONCE_SALT) set to `Options.Salt()` (default `pseudo.Salt`) | status only (`on` when one is missing or the sample phrase) | |
| `side-effect.mail` | Echo connector, `Settings{Mode: seal, URL: --echo-url, Proxy: --echo-proxy}`: local sites get `wordpress.Files` written through Site.WriteFile (0644) to `wp-content/mu-plugins/rhythm-echo.php` and `wp-content/mu-plugins/rhythm-echo/settings.php` (the same files `wordpress.Install` writes; through the Site so read-only and tests apply); ssh/alias pipe `wordpress.Script` into `wp eval-file -` | critical `side-effect.mail` with neither connector nor `pre_wp_mail` block, or a mailer option not on its default mailer with a secret set; high `side-effect.connector-changed` when the file's SHA-256 differs from `wordpress.Plugin()` | `delete wp-content/mu-plugins/rhythm-echo.php (roadie-staging.php then blocks wp_mail)` |
| `side-effect.action-scheduler` | writes `MuPlugin(o)` to `wp-content/mu-plugins/roadie-staging.php` (0644) | high when the mu-plugin hooks are missing and pending actions > 0; How carries `; <n> pending actions kept, never run` | `delete wp-content/mu-plugins/roadie-staging.php` |
| `side-effect.jetpack` | (mu-plugin, DataRule) | medium when Jetpack is present (option `jetpack_options` or `wp-content/plugins/jetpack`) and not offline | `delete ... and reconnect Jetpack` |
| `side-effect.webhooks` | (DataRule) | high with active rows in `wc_webhooks` (count) | `WooCommerce > Settings > Advanced > Webhooks` |
| `side-effect.gateways` | (DataRules) | critical per enabled known gateway not in test mode and per enabled unknown gateway (`SourceGatewayIDs`: the keys of `woocommerce_gateway_order` plus every gateway-shaped `woocommerce_<id>_settings`, so a store that never saved its gateway order is covered) | `WooCommerce > Settings > Payments` |
| `side-effect.automatewoo` | (DataRule) | high with rows in `automatewoo_queue` | |

A foreign file is never replaced (exit 1 `kill-switch`):

- `side-effect.mail: wp-content/mu-plugins/rhythm-echo.php exists and wasn't written by Echo, so mail from this copy isn't sealed. Remove it, then: rhythm-roadie prepare <display> --only side-effects.`
- `side-effect.action-scheduler: wp-content/mu-plugins/roadie-staging.php exists and wasn't written by Roadie, so Action Scheduler, Subscriptions renewals and Jetpack aren't paused. Remove it, then: ...`
- Write failures: `<step>: couldn't write <file> (<cause>), so <meaning>. Fix it, then: rhythm-roadie prepare <display> --only side-effects.` Meanings: `WordPress may still call this copy production`, `WP-Cron may run production's scheduled events`, `WP_HOME or WP_SITEURL may still name production`, `live keys stay in wp-config.php`, `logins from production stay valid here`, `mail from this copy isn't sealed`.

Config `side_effects`: `cron: keep`, `webhooks: keep`, `gateways: keep`, `subscriptions: keep`, `mail: keep` skip their edits and rules (plan line `left on (side_effects.<key>: keep)`); `action_scheduler: cancel` adds the cancel rule; `echo.install: false` skips the connector.

`DataRules(wordpress, o)` (profile `killswitch`, tables `{prefix}...`; option rule IDs `killswitch.options.option_value.<option>.<leaf>`):

- each gateway: `ModePath` Fixed `TestValue`; each `LiveSecretPaths` SwapKey; each ID in the new `Options.GatewayIDs` (fill it with `SourceGatewayIDs` over the source's options) the list doesn't know (by `ID` or `woocommerce_<id>_settings`): `woocommerce_<id>_settings` `enabled` Fixed `no`
- each mailer: `MailerPath` Fixed `DefaultMailer`; `SecretPaths` Blank
- `killswitch.wc_webhooks.status` Fixed `paused`, `killswitch.wc_webhooks.secret` Blank
- `killswitch.automatewoo_queue` and `killswitch.automatewoo_queue_meta` Truncate
- Jetpack: `jetpack_private_options` and `jetpack_options` leaves `blog_token`, `user_tokens` Blank
- with `action_scheduler: cancel`: `killswitch.actionscheduler_actions.status` Where `status: pending` Fixed `canceled`

New exported names: `Options.Version`, `Options.Salt`, `Options.GatewayIDs`, `GatewayIDs(order []byte)`, `SourceGatewayIDs(opts map[string][]byte) []string`, `GatewayOptionsMatch(name string) bool`, `ManualPrefix` (`"manual: "`), `EchoPluginPath`, `SendmailLine` (`sendmail_path = "/usr/local/bin/rhythm-echo sendmail -t -i"`), `Salts`, `Profile`, `MuPluginPath`, `MuPluginMarker`, `Crawlers`, `RobotsTxt()`.

Plan output (golden `site/killswitch/testdata/golden/plan.txt`) lists one PlannedEdit per change, including `side-effect.mail | php.ini | manual: sendmail_path = "/usr/local/bin/rhythm-echo sendmail -t -i" (the path of rhythm-echo on this host), so PHP's mail() reaches Echo too`. PlannedEdit has no kind field, so "manual" is the `ManualPrefix` on What.

## 8. The Roadie mu-plugin (`roadie-staging.php`)

Header: `Plugin Name: Roadie staging guard`, a description line and `Roadie (rhythm-roadie <version>) wrote this file. Delete it to turn the guard off.` (`dev` without a version). Returns early when `wp_get_environment_type()` is `production` (and runs when the function is missing). Hooks (priority 999 unless noted):

- `action_scheduler_allow_async_request_runner` `__return_false`; `action_scheduler_queue_runner_concurrent_batches` returns 0; on `init` priority 20 `remove_action( 'action_scheduler_run_queue', array( ActionScheduler_QueueRunner::instance(), 'run' ) )` when the class exists (names checked against Action Scheduler trunk)
- `woocommerce_subscriptions_is_duplicate_site` `__return_true`; `jetpack_offline_mode` `__return_true`
- `pre_wp_mail` returns false when `rhythm-echo.php` is not next to it
- with noindex: `wp_robots` adds noindex and nofollow; `send_headers` sends `X-Robots-Tag: noindex, nofollow`; `pre_option_blog_public` returns `'0'`; `wp_sitemaps_enabled` false; `robots_txt` returns `killswitch.RobotsTxt()`: `User-agent: *` / `Disallow: /` then a block per `GPTBot`, `ClaudeBot`, `CCBot`, `Google-Extended`, `Bytespider`, `PerplexityBot`, `Amazonbot`, `Applebot-Extended`
- `admin_bar_menu` (100) node `roadie-staging` titled `Pseudonymised staging copy (Roadie)`

Tests: goldens for default and no-noindex; `php -l`; a PHP harness (`testdata/harness.php`) that loads the plugin with WordPress stand-ins and checks the hooks, the production early return and the Echo-present case. Nightly only: `site/killswitch/muplugin_docker_test.go` (build tag `docker`) boots `wordpress:6.8.3-php8.3-apache`, `mysql:8.4` and `wordpress:cli-2.12.0-php8.3`, installs WooCommerce 9.8.5 and the woocommerce-subscriptions-core 8.2.0 source zip (each checked against a pinned SHA-256, read only from `ROADIE_PLUGIN_ZIPS=<folder>` of `<slug>.<version>.zip` files; the test skips without it and never downloads) and loads subscriptions-core through a small test plugin the way WooCommerce Subscriptions does. It greps the four hook names in those sources. Controls without the mu-plugin: `WCS_Staging::is_duplicate_site()` is false once the URL lock is set to the site; a due action runs through a page load and `wp cron event run --due-now`. With the mu-plugin: a due action and a due `woocommerce_scheduled_subscription_payment` renewal stay pending through a page load and cron, `WCS_Staging::is_duplicate_site()` is true and `blog_public` reads 0. (subscriptions-core 8.2.0 has no `wcs_is_duplicate_site()` function; `WCS_Staging::is_duplicate_site()` applies the filter.) Ran here with local zips: pass (about 3 minutes); the containers, volume and network are removed after.

## 9. Exposure guard (`site/exposure`)

`exposure.Steps(o)` returns one step, `search.indexable` (phase `files`). `exposure.AllSteps(stack, o)` returns `killswitch.Steps(stack, o)` followed by `exposure.Steps(o)` for every stack (on a sql target the robots.txt step reports `not-present`); the engine should call it so the robots.txt step is never left out. The noindex hooks themselves are in `roadie-staging.php` (section 8); this step handles the physical `robots.txt`, which wins over WordPress's `robots_txt` filter.

- Web root: `robots.txt` for WordPress, `public/robots.txt` for Laravel; a sql target has none (the step plans and writes nothing; its Check is `not-present`).
- Apply with `noindex: true` (`killswitch.Options.Noindex`): a physical `robots.txt` is copied to `robots.txt.roadie-backup` (only when no backup exists, so a second run never overwrites the site's own file with Roadie's) and replaced with `killswitch.RobotsTxt()`: `User-agent: *` / `Disallow: /` then the same block for `GPTBot`, `ClaudeBot`, `CCBot`, `Google-Extended`, `Bytespider`, `PerplexityBot`, `Amazonbot`, `Applebot-Extended` (LF line ends, mode 0644). A file already equal to it is left alone. WordPress without a physical file: nothing is written (the mu-plugin serves the same text). Laravel without one: `public/robots.txt` is written, since Laravel has no filter to serve it (deviation from the lane text, which only covers WordPress).
- `--noindex=off`: plan line `left on (--noindex=off): no noindex, robots.txt or crawler block`, nothing written; the Check still runs.
- Plan lines: `wp-content/mu-plugins/roadie-staging.php | noindex and nofollow (wp_robots and X-Robots-Tag), robots.txt from the robots_txt filter, sitemaps off; blog_public in the database left as it is` (WordPress); `robots.txt | backed up to robots.txt.roadie-backup and replaced: everything disallowed for every agent and the named AI crawlers`, or `replaced (the backup robots.txt.roadie-backup from an earlier run is kept): ...`, or `already blocks every crawler`; Laravel without a file: `public/robots.txt | written: everything disallowed for every agent and the named AI crawlers`.
- Write failure (exit 1, `kill-switch`): `search.indexable: couldn't write <file> (<cause>), so search engines may still crawl this copy. Fix it, then: rhythm-roadie prepare <display> --only noindex.`
- Check, finding `search.indexable` (medium, fix `rhythm-roadie prepare <staging> --only noindex`), status `on`:
  - WordPress with a physical robots.txt that doesn't block, no Roadie mu-plugin holding `wp_robots` and `blog_public` not `0`: `robots.txt lets crawlers in; a physical robots.txt wins over WordPress's own`;
  - WordPress with no physical file, no Roadie mu-plugin holding `wp_robots` and `blog_public` not `0`: `no noindex from roadie-staging.php, blog_public is 1 and no robots.txt blocks crawlers`;
  - Laravel with a `public/robots.txt` that doesn't block: `public/robots.txt lets crawlers in`;
  - Laravel with no `public/robots.txt`: `no public/robots.txt and nothing else tells crawlers to stay out`.
  - Off (no finding): the mu-plugin noindex, a blocking physical file, or `blog_public` 0 (WordPress); a blocking `public/robots.txt` (Laravel). Pages that say noindex stay out of the index even when a physical robots.txt lets crawlers read them, so the mu-plugin or `blog_public` 0 with an open robots.txt is off, How `roadie-staging.php sends noindex; robots.txt lets crawlers in, which a physical robots.txt does over WordPress's own` (as the lane spec says: a finding only when neither the noindex nor a blocking robots.txt is there). Apply still replaces that file. Undo texts: `delete wp-content/mu-plugins/roadie-staging.php`, `restore robots.txt from robots.txt.roadie-backup`, `Settings > Reading > Search engine visibility`.
- If Roadie can't tell whether `robots.txt.roadie-backup` exists (an ssh failure), Apply writes neither file and stops with the write error above for `robots.txt.roadie-backup`, so the first run's backup is never replaced.
- "Blocks" (exported `exposure.Blocks`): the file has a `User-agent: *` group and every group, for any agent, holds `Disallow: /` and no `Allow:` rule with a path. A crawler obeys only the group that names it; RFC 9309 merges groups for the same agent. So `User-agent: Googlebot` / `Allow: /` after a blocking `*` group opens the file; so does a trailing group of user-agent lines with no rules. Comments, CRLF, a leading BOM and `Sitemap:` or `Crawl-delay:` lines are read; rules before any user-agent line belong to no group. Fuzz target `site/exposure:FuzzBlocks`.

Basic auth (`exposure.BasicAuth(user)`, for `--basic-auth <user>`):

- Password: 20 letters and digits from `crypto/rand`, each drawn evenly (about 119 bits). Returned once for the caller to print; it is in no file.
- User: 1 to 64 letters, digits or `.`, `_`, `-`, `@` (exactly what the message says). Else a usage error (exit 2): `basic-auth: user "<user>" can't be used: give 1 to 64 letters, digits or . _ - @ (no ':', spaces or quotes)`.
- Files (map keys; added constants `exposure.FileApache`, `FilePasswd`, `FileNginx`, `FileCaddy`):
  - `.htpasswd`: `<user>:$2y$05$...` (bcrypt cost 5, htpasswd -B's default: the server checks it on every request and the password is random);
  - `apache.htaccess`: `AuthType Basic`, `AuthName "Staging copy"`, `AuthUserFile /etc/roadie/staging.htpasswd`, then `<RequireAny>` with `Require ip 127.0.0.1 ::1` and `Require valid-user`;
  - `nginx.conf`: `satisfy any; allow 127.0.0.1; allow ::1; deny all; auth_basic "Staging copy"; auth_basic_user_file /etc/roadie/staging.htpasswd;`;
  - `Caddyfile`: `@notLocal not remote_ip 127.0.0.1 ::1` and `basicauth @notLocal { <user> $2a$05$... }` (the hash inline; Caddy needs no .htpasswd).
  - Each starts with comments saying where the lines go, that `.htpasswd` goes to `/etc/roadie/staging.htpasswd` outside the web root (nginx and Caddy would serve it from inside one) and that loopback requests pass so WP-Cron spawns and Site Health work (Apache: add the server's own address to `Require ip` when loopbacks go out through its public IP). Each also says: `# Behind a reverse proxy on this host every request is loopback: remove` / `# the loopback allow lines there.` (nginx in front of Apache, a tunnel or Caddy proxying over HTTP makes every visitor 127.0.0.1, so the password would never be asked). Constant `exposure.PasswdFile`.
- Added `exposure.WriteBasicAuth(dir, files)`: writes them under `<dir>/.roadie/basic-auth/` (constant `exposure.BasicAuthDir`), folder 0700 (chmod also when it existed), files 0600; refuses a name with a path.
- Checked against real servers on 127.0.0.1 ports: Apache 2.4 (`php:8.3.27-apache`) and nginx (alpine and Debian) answer 401 without credentials, 401 with a wrong password, 200 with the generated one; a loopback request with none gets 200. The Caddy snippet was not run here (no image or module download from this machine).

## 10. Laravel and database-only kill switch

`killswitch.Steps(laravel, o)`, in order, all phase `files`, every edit through `site/dotenv` on `laravel.env_file` (default `.env`), written only when it changed (mode kept; 0640 for a new file):

| Step ID | Apply (.env) | Check | Undo |
|---|---|---|---|
| `config.environment` | `APP_ENV=staging`; `APP_URL=<--url without trailing slash>` (when `--url` is set); then every `laravel.env` key, in name order | critical when `APP_ENV` is missing, empty, a `${...}` reference or not one of `staging`, `local`, `development`, `testing`: `APP_ENV isn't set, which Laravel reads as production` / `APP_ENV isn't staging` | `set APP_ENV in .env` |
| `config.salts` | `APP_KEY` from `Options.AppKey` (default `pseudo.AppKey`: `base64:` and 32 random bytes; written double quoted) unless `--keep-app-key` | status only: `on` when APP_KEY is missing or kept (`APP_KEY was kept from production (--keep-app-key)`); equal to production is verify's finding | `copy APP_KEY back from production (not recommended)` |
| `side-effect.mail` | `MAIL_MAILER=smtp`, `MAIL_HOST=127.0.0.1`, `MAIL_PORT=1025`, `MAIL_USERNAME=`, `MAIL_PASSWORD=`, `RHYTHM_ECHO_URL=<--echo-url, default http://127.0.0.1:8025>`, `RHYTHM_ECHO_MODE=seal`; `MAIL_URL=`, `MAIL_SCHEME=`, `MAIL_ENCRYPTION=` only when the file has them (MAIL_URL wins over MAIL_HOST; TLS to the sink would fail) | critical (`.env` and line): `RHYTHM_ECHO_MODE is <x>, so Echo lets messages out`; `MAIL_URL names a mail server other than Echo's sink`; smtp (also when MAIL_MAILER is unset) with a non-loopback `MAIL_HOST` and `MAIL_USERNAME` or `MAIL_PASSWORD` set: `MAIL_HOST is <host> with credentials set, so mail from this copy reaches real people`; `mailgun`, `postmark`, `ses`, `ses-v2`, `resend`, `sendgrid` with their key set (`MAILGUN_SECRET`, `POSTMARK_TOKEN`/`POSTMARK_API_KEY`, `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`, `RESEND_KEY`/`RESEND_API_KEY`, `SENDGRID_API_KEY`): `MAIL_MAILER is <m> and <KEY> is set, ...`. Off: loopback host (`127.0.0.1`, `localhost`, `::1`), `log`, `array`. Unknown: a non-loopback host without credentials, other mailers | `set MAIL_* in .env back to your mail server` |
| `side-effect.queue` | `QUEUE_CONNECTION=sync` | `sync`, `null`, `log`: off. `database` (also when unset, Laravel 11's default): status on; high finding `<n> queued jobs from production wait for a worker on this copy` with the count when `<DB_PREFIX>jobs` has rows. Other connections: unknown (`that queue isn't in the database, so Roadie can't count its jobs`) | `set QUEUE_CONNECTION in .env and start a worker` |
| `key.generic` | live keys in `.env` swapped or blanked (keys.ScanPairs on `.env`, keys.Replace with `keys.mode`) | `key.<provider>-live` per live key left, masked example | |

- `laravel.env` keys win: a step never overwrites a key the config sets; the plan shows `NAME=value (laravel.env)`.
- `side_effects.mail: keep`: plan `left on (side_effects.mail: keep)`, MAIL_* untouched.
- Plan lines (golden `site/killswitch/testdata/golden/laravel-plan.txt`): `config.salts | .env | APP_KEY=base64: and 32 random bytes: encrypted columns and old sessions from production become unreadable (pass --keep-app-key to keep the key)`; with `--keep-app-key`: `APP_KEY kept (--keep-app-key): it is production's key, so encrypted columns and old sessions stay readable only with it; this copy can also read what production encrypts`. Manual lines (What starts with `manual: `): `side-effect.mail | composer.json | manual: composer require rhythmsh/echo-laravel --dev (Roadie never runs Composer), so Echo seals mail, SMS and webhooks in the app too` and `side-effect.queue | queue workers | manual: Stop queue workers and the scheduler on this host: jobs from production were cleared, new ones run in the request (QUEUE_CONNECTION=sync) through Echo.` Constants `killswitch.ComposerLine`, `WorkersLine`, `EchoSMTPHost`, `EchoSMTPPort`.
- Write failures: `<step>: couldn't write .env (<cause>), so <meaning>. Fix it, then: rhythm-roadie prepare <display> --only side-effects.` Meanings: `Laravel may still call this copy production`, `this copy keeps production's APP_KEY`, `mail from this copy isn't sealed`, `queued jobs may run here`, `live keys stay in .env`.
- `DataRules(laravel, o)`: `killswitch.jobs` and `killswitch.job_batches` Truncate (tables `jobs`, `job_batches`, no prefix placeholder), so the "jobs were cleared" line holds whatever the profiles say.
- Added `killswitch.Options.AppKey func() string` (nil: `pseudo.AppKey`; `""` gives `core.ErrNotBuilt`).

Sql targets (a DSN, or a folder with neither stack): `DataRules(sql)` is nil. `Steps(sql)` returns one check-only step `side-effect.not-applicable` (deviation from the lane text "returns none": without a step the engine has nothing to call Check on). Plan line: `This target is a database only: Roadie can't install the Echo seal or edit config files. Turn mail and jobs off where the app runs.` (`killswitch.DatabaseOnlyLine`); Apply writes nothing; Check: status `unknown` and an info finding `side-effect.not-applicable` with that sentence.

## 11. Kill switch against WordPress in Docker (nightly only)

`site/integration_docker_test.go` (build tag `docker`; skipped without Docker): `wordpress:6.8.3-php8.3-apache`, `mysql:8.4`, `wordpress:cli-2.12.0-php8.3`, WooCommerce 9.8.5, WP Mail SMTP 4.10.3 (SMTP to a sink container that logs every connection on 2525 and 8080), a fixture plugin that mails on every `init` and from a cron hook, an active WooCommerce webhook to the sink plus Echo (`rhythm-echo up --seal`, built for Linux from the Echo module Roadie depends on) in its own container. Every container is on a private network; nothing is published on the host.

1. Control: without the kill switch a page load reaches the sink by SMTP and the webhook by HTTP (the test fails here if the fixture can't leak, so it proves something).
2. Roadie's WordPress Steps and exposure Steps applied through `site.Open` on the bind-mounted files; the DataRules applied to the database by a small PHP applier. A page load, a new order and `wp cron event run --due-now` reach the sink zero times; Echo holds the `web` and `cron` mails; `wc_webhooks.status` is `paused`.
3. Echo stopped: `wp_mail` returns false (fail closed) and the sink still sees nothing.

`ROADIE_PLUGIN_ZIPS=<folder>` is required (the test skips without it and never downloads): it installs `woocommerce.9.8.5.zip` and `wp-mail-smtp.4.10.3.zip` from that folder, each checked against a pinned SHA-256. `site/testdata/fetch-plugin-zips.sh <folder>` fetches and checks all three pinned zips for the nightly workflow, in a step of its own. Run here with local zips: pass. WooCommerce 9.8.5's `wc_admin_daily` fatals on the answer Echo fakes for woocommerce.com, so the test deletes that event before the sealed cron run.
