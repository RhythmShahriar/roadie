# Lane cli: what users see

The CLI (`cmd/rhythm-roadie`), the reports (`report`), the GitHub Action (`action.yml`, `scripts/`) and the README, as built for Roadie 1.0. The docs team writes the wiki pages of PRD 23 from this file; PRD section numbers point at the full text where this file only names a thing.

## Commands

`rhythm-roadie` and `rhythm roadie` are the same program. Help, version, update, Ctrl-C, the update prompt and completion come from kit's `cli.Main`.

The background release check (`A new release of rhythm-roadie is available: <old> → <new>`, `run: rhythm update`) runs only when stderr is a terminal. It is off, with no request to the release API, for `--json`, `-q`/`--quiet`, `--offline`, `RHYTHM_OFFLINE`, when `CI` is set and when stderr is piped.

| Command | What it does |
|---|---|
| `rhythm-roadie` | the picker on a terminal (stdin and stdout both terminals); otherwise the top help, exit 0 |
| `copy --from <source> --to <target>` | copy production to staging: pseudonymise, swap keys, turn side effects off, verify the result |
| `prepare <staging>` | the same pipeline in place on an existing staging copy, after a backup |
| `verify [staging]` | look for real people, live keys, production URLs and side effects; signed pass or fail report |
| `plan --from <source> [--to <target>]`, `plan <staging>` | dry run of copy or prepare; every change, none made; writes no report |
| `config init [--stack s] [--from <source>] [--force]`, `config check`, `config show [--json]`, `config key show\|rotate\|forget` | the settings file and the consistency key |
| `report <report.json>... -o <file>`, `report check <report.json>` | render saved reports; check a signature |
| `profiles [name] [--json]` | the stack profiles and their rules |
| `explain <id>` | the offline explanation of a finding, rule family or error code |
| `demo [--keep]` | the whole loop on a built-in shop, offline |
| `update [--json] [--pin profiles=<v>] [--offline] [--clear]` | refresh the signed profile pack; `--offline` reports the pack in use; `--clear` drops the cached pack |
| `version [--json]`, `--version` | the version (`rhythm-roadie 1.0.0`; `dev` for a local build) |
| `help [command]`, `<command> --help` | PRD 6.1's text, or the command's block plus the shared Output, Settings and exit-code blocks |

The top help is PRD 6.1's text exactly. Each command's help is in `cmd/rhythm-roadie/testdata/golden/help-*.txt`; those goldens are the reference for the CLI Reference page.

## Flags and defaults

Flags parse anywhere on the line; `--` ends them. Every command takes the global flags of PRD 6.3: `--json`, `--md`, `--html`, `--sarif`, `--junit`, `-o`/`--output` (repeatable; the extension picks the format: `.json .md .html .sarif .xml`), `--open`, `-q`/`--quiet`, `--plain`, `--no-color`, `-v`/`--verbose`, `--debug`, `--config`, `--no-config`, `--offline`.

Additions to the PRD's help blocks, in their style:

- copy and prepare, `Where:`: `--dry-run              run plan with the same arguments: every change, none made`
- copy, prepare and plan, `Target safety:`: `--production <list>    production hosts or URLs, e.g. shop.com,*.shop.com (adds to` / `production: in the config); a target that matches is refused`
- copy and prepare, `Target safety:`: `--max-output-ratio <n> stop when the dump grows past n times its compressed size` / `(default 20)`
- copy, prepare and verify: `--sign=off             don't sign the JSON report (default: sign it)` and `--key <file>           sign with this Ed25519 key instead (ROADIE_SIGNING_KEY in CI)`
- `Keys and side effects:`: `--noindex=off          don't add noindex, robots and the crawler block (default on)`
- plan: `--schema-only` and `--fail-on` with `none (default)`

Defaults: `--email-domain example.invalid`, `--keys swap`, `--echo-url` from `RHYTHM_ECHO_URL` else `http://127.0.0.1:8025`, `--jobs` the number of CPUs at most 8, `--max-memory` none (the engine sets 300MiB unless `GOMEMLIMIT` is set), `--max-value 256MiB`, `--max-output-ratio 20`, `--fail-on high` for copy, prepare and verify (`verify.fail_on` in the config fills it), `--fail-on none` for plan (only its own flag changes it), signing on, noindex on.

A flag wins over the config, the config over the default. The config fills `source`, `target`, `url`, `url_map` (merged pair by pair), `stack` (`auto` detects), `production` (`--production` adds to it on every command), `pseudonymise.keep_users`, `email_domain`, `consistency: stable` (as `--keep-key`), `drop`, `truncate`, `keys.mode`, `side_effects.echo.url` and `proxy`, `noindex: false`, `basic_auth: true`, `verify.allow_emails`, `allow_phones`, `skip`, `sign: false`, `keys.ignore` and `verify.ignore`.

`--offline` and `RHYTHM_OFFLINE` (anything but empty or `0`) use the built-in or cached profiles and keep every update check off.

Errors that quote a database address never show its password: `mysql://user:pw@host/db` is printed as `mysql://user@host/db` and any other copy of the password, raw or URL-decoded, as `•••` (copy, prepare, plan, verify and `config init --from`).

Usage errors exit 2 with `rhythm-roadie <cmd>: <message>` and `  run 'rhythm-roadie help <cmd>' for usage`; an unknown flag adds `  did you mean: rhythm-roadie copy --from`. The full list of messages is in the lane notes (`review/2026-10b/roadie/changes-cli.md`, batch 1, item 2).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done and verify found nothing at or above `--fail-on` |
| 1 | verify found something at or above `--fail-on`, plan would stop (with `--fail-on` set), the copy stopped on uncovered columns, or `report check` found a changed report or another key or input |
| 2 | usage error: a wrong command line or a bad `.roadie/config.yaml` |
| 3 | couldn't run: source or target unreachable, a dump that ends early, a target refused as production, nothing to scan, a report that can't be built, signed or saved, an unsigned report in `report check` |
| 130 | interrupted |

The error codes and their messages are PRD 17's; `explain <code>` has each.

## Screens

Reference layouts: `cmd/rhythm-roadie/testdata/golden/screen/*.txt` (plain and colour). The pinned strings of PRD 16.1 are all there:

- `PASS   <host> is a pseudonymised copy` only when the checks found nothing above info; otherwise `PASS   <name>: 27 findings, none fails the run (--fail-on none)`, `PASS   <name>: 3 findings below --fail-on high` and `...; 12 findings hidden by --baseline or ignore entries`. `PASS (sample)   ...`, `FAIL   <name> is not a safe copy yet`, `PLAN   copy would stop: <n> uncovered column(s)`, `PLAN   ready: nothing changes until you run copy`, `DONE   copied to <name>; not checked (--no-verify)`, `DONE   prepared <name>: <parts>; not checked (--only noindex reads no data|--no-verify)`, `STOPPED   <code>; the target is untouched|is in a mixed state|was replaced` (an error without a code shows the state alone; a failed remote command is `ssh`, a table the database lacks `site-config`)
- `<n> critical · <n> high · <n> medium · <n> low · <n> info`
- `Pseudonymised, not anonymised. The copy still holds order totals, dates, products and anything Roadie can't recognise in free text. Treat it as personal data.` (after every copy, prepare and verify; not with `-q`)
- `Mail from this copy goes to Echo at <url>. Run rhythm echo up on the staging host, or the copy's mail will fail closed.`
- `→ signed report: <path>` and `→ next: rhythm echo up, then rhythm soundcheck run <url>` (the next step names the other tools by their router commands)
- `▸ Mode          exact: every value Roadie replaced was checked` / `▸ Mode          heuristic: this copy wasn't made by Roadie in this run`
- the prepare question, `config init`'s three lines and the first-run note of PRD 6.11

UNCOVERED in plan and on copy's stop screen titles each column as the finding does everywhere else, `contacts.emergency_contact looks personal (100% of 100 sampled values are phone numbers) and no rule covers it` (or `(by its name)`), with the fix `Add the column under columns: in .roadie/config.yaml (rhythm-roadie explain uncovered-column).`

A wrapped check detail in the VERIFY block lines up under the first line of its detail. Every check has a readable label (`No secret-looking settings`, `Off: WP-Cron`; `Secret-looking settings`, `WP-Cron` when it warns or fails), never its ID. The Markdown checks table gives one total per failing check: `❌ 280 hits in 8 columns`.

The ALSO block's rsync line for uploads passes a non-default SSH port as `-e 'ssh -p N'`. With SSH on both ends it runs rsync on production and pushes to staging: `ssh [-p N] user@prod 'rsync -a [-e '\''ssh -p M'\''] --exclude ... /prod/path/ user@staging:/staging/path/'`.

`--basic-auth <user>` (copy and prepare) prints `  Basic auth for <user>: password <password> (shown once; <dir> holds only its hash)`. With `GITHUB_ACTIONS=true` it prints `::add-mask::<password>` first, writes the password to `.roadie/basic-auth/password` (0600, folder 0700, an older file replaced) and prints `  Basic auth for <user>: password in .roadie/basic-auth/password (0600; the job log masks it)`, or `  Basic auth for <user>: couldn't save the password (<reason>); run --basic-auth again`. Help: `--basic-auth <user>` ends `printed once (default off; on GitHub Actions it is masked and kept in .roadie/basic-auth/password). The user is 1 to 64 letters, digits or . _ - @`.

`-q` prints only the box's two lines; `-v` every table, rule and file edit; `--plain` no colour, boxes or glyphs (`[ok]`, `[FAIL]`); `--debug` timings and counts, never values. Progress on a terminal: `Streaming   ████░░…  1.9 GB of 2.4 GB · 31 MB/s · 0:14 left`.

## Picker

`What should Roadie do?` with PRD 6.12's six lines. copy asks `Where is production?`, `Where should the copy go?` and `The copy's URL?`, runs plan, asks `Copy now? [y/N]`, copies, then (only when there is no config) `Save these answers to .roadie/config.yaml? [Y/n]` and prints `Wrote .roadie/config.yaml for <stack words> with source:, target: and url: set.` (the template of the stack the copy found; WordPress when none) and `Added .roadie/reports, .roadie/key, .roadie/backups and .roadie/basic-auth to .git/info/exclude.` A DSN's password is never saved: `mysql://u:pw@h/db` is written as `mysql://u@h/db` with `Left the password out of source: in .roadie/config.yaml; set ROADIE_FROM_PASSWORD when you run Roadie.` (`target:` and `ROADIE_TO_PASSWORD` likewise). prepare and verify ask `Which staging site?`; config init asks `Which stack?`. Esc or Ctrl-C exits 130.

## JSON (`--json`, schema `rhythm-roadie/v1`)

Envelope: `schema`, `kind` (`plan`, `copy`, `prepare`, `verify`, `config`, `profiles`, `explain`, `report-check`, `demo`), `tool` (`rhythm-roadie`), `version`, `complete`, `summary`, `verdict` (`ok`, `findings`, `error`), `data`.

- `summary`: `critical`, `high`, `medium`, `low`, `info`, `tables`, `tables_pseudonymised`, `rows`, `values_pseudonymised`, `urls_rewritten`, `keys_found`, `keys_swapped`, `keys_blanked`, `side_effects_off`, `side_effects_total`, `uncovered_columns`.
- `data` for copy, prepare, verify and plan: every field of PRD 16.2 in snake_case. `data.result` is `pass`, `fail`, `error` or `unchecked` (a copy or prepare verify didn't read: `--no-verify` or a stdout target; never `pass`); `complete` is false for a sampled verify, an unchecked run, a run that stopped or one that scanned nothing; `not_covered` is always present; a run that stops has `data.error: {code, message, exit}`. Example: `report/testdata/golden/envelope-copy-pass.json`.
- Canonical JSON and signing refuse a float, even a whole one: `canonical JSON: a float (float64) where Roadie writes integers only` and `report: a float (float64) where Roadie writes integers only`. No report Roadie writes holds one.
- `report check --json`: `kind: "report-check"`, `data: {result: valid|edited|unsigned|error, key_id, input_checked, error?}`.
- `profiles --json`: `data: {profile_pack, profiles: [...]}`; `explain --json`: `data: {id, text}`.

## Reports

- Every copy, prepare and verify saves `.roadie/reports/<UTC 2006-01-02T15-04-05>/report.json` (signed) and `report.html` (files 0600, folders 0700); `-2`, `-3` when two runs start in the same second; `<cache>/roadie/reports/<ts>/` when the project folder can't be written.
- Markdown, HTML, SARIF 2.1.0 and JUnit as PRD 16.3 describes; each carries `Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26).` and the Not covered sentence. SARIF rules link to `https://github.com/RhythmShahriar/rhythm/wiki/Roadie-Verify-And-Reports#<id>`. SARIF `artifactLocation.uri` keeps a relative path relative (`./` in front when its first segment holds a colon) and writes an absolute one as a `file:` URI: `/srv/dumps/staging.sql` is `file:///srv/dumps/staging.sql`, `C:\dumps\staging.sql` is `file:///C:/dumps/staging.sql`, `\\nas\share\x.sql` is `file://nas/share/x.sql`.
- `report <report.json>... -o <file>` prints `Wrote <path> (<n> report[s]).`; `--brand-name` and `--brand-logo` (PNG, SVG or JPEG under 512 KiB) on HTML.

## Signing and `report check`

- Key: `ROADIE_SIGNING_KEY` (base64 of a 32-byte seed), else `--key <file>`, else `<config dir>/roadie/signing.key`, made on first use with `Created a signing key for Roadie reports: roadie:7Q3kXc2… (keep <path> private; set ROADIE_SIGNING_KEY in CI to sign with one identity).`
- Key ID `roadie:` plus 16 characters; on screen 7 and `…`.
- `report check <report.json> [--key <id|file>] [--input <dump>]`: `signed by roadie:7Q3kXc2… and unchanged since. It shows this Roadie run found these results; it doesn't prove the checks covered everything.` (exit 0); unsigned exit 3; edited, another key or another input exit 1. Exact messages: lane notes, batch 2, item 6.
- `--key` takes a key ID (`roadie:...`), a report's `public_key` (a bare 32 bytes, base64 or raw, is always read as a public key, never as a seed) or a PEM Ed25519 key (`PUBLIC KEY`, or `PRIVATE KEY` for its public half). A `signing.key` file is not a public key: pin your own key by its key ID. Another value exits 2: `report check: --key isn't a public key: give a key ID (roadie:...), a report's public_key or a PEM Ed25519 key`. A report signed by another key exits 1: `report check: report.json is signed by <ID>, not by the key you pinned (<ID>).` When the pinned bytes are the seed of the signing key, the message adds `If --key is a signing.key file, that is a private key, not a public one: pin a key ID (roadie:...) or a report's public_key you trust; keep signing.key private.` (the pin is still never trusted). `--key` naming this machine's own `signing.key` exits 2: `--key <path> is Roadie's private signing key, not a public key; pin the key ID report check prints (--key roadie:...)`. A valid report adds `Key ID roadie:<16>: pin it with --key roadie:<16> to accept only this signer.`

## config, profiles, explain, demo

- `config init`: `Wrote .roadie/config.yaml for a WooCommerce site.` / `Set source:, target: and url: in it, then run rhythm-roadie plan.` / `Added .roadie/reports, .roadie/key, .roadie/backups and .roadie/basic-auth to .git/info/exclude.`; `--from` lists columns that look personal as commented suggestions; CR, LF, NEL, U+2028 and U+2029 in a table or column name become a space there, so a name can't end the comment.
- `config check`: `<path> is valid.` and `Unset: ROADIE_X (line n); set it before a run that reads it.` for a `${ROADIE_*}` in a value, never for one in a trailing comment.
- `config check` also warns about a database password written into `source:` or `target:` (exit 0): `Password: source: (line 2) holds the database password, which other users of this machine can see. Remove it and set ROADIE_FROM_PASSWORD.` One read from a `${ROADIE_*}` variable doesn't count. `--json` lists them under `data.passwords` as `{key, line, env}`.
- The inline-password warning names where the DSN came from: `--from`, `--to`, `the <staging> argument`, or `source: in .roadie/config.yaml` / `target: in .roadie/config.yaml` (the `--config` path when one is given).
- `config init --from` reads a dump with triggers, functions or views without `--keep-routines`: it only reads the schema and never runs or writes the dump, as verify.
- `config key show|rotate|forget` messages: lane notes, batch 1, item 5.
- `profiles` lists the 24 profiles of PRD 6.10 with what each was checked against; `profiles <name>` and `config show` print a rule whose address part `pseudonymise.keep` names as `kept (keep: country)` (`"kept": "country"` in JSON); `explain` has an offline text for every finding ID, live-key rule, error code and profile, plus `pseudonymised`.
- `demo`: a 40-customer shop with fake people only, planned, copied, the copy verified (PASS) and the shop verified (FAIL), in a 0700 folder deleted at the end unless `--keep`. No network. `demo --json` that can't run prints `rhythm-roadie: <reason>` once on stderr with its exit code and nothing on stdout.

## update and the router

- `update` and `update --pin` are kit's, backed by `profile.Update` and `profile.UpdatePinned`; `--json` prints `{"schema":"rhythm-data/v1","data":[{"name":"profiles","version":"<v>","sha256":"<hex>"}]}`.
- New: `update --offline` (or `RHYTHM_OFFLINE` set) downloads nothing and reports the pack in use in the same shape; the sha256 is the digest of the profiles in use. Text mode prints `profiles <version>` and, on stderr, `Offline: nothing was downloaded. This is the profile pack Roadie uses now.` `--pin` with `--offline` exits 2: `rhythm-roadie update: --pin downloads the pinned pack, so it can't run offline: drop --offline (and unset RHYTHM_OFFLINE)`. A pack that can't load exits 3 `rhythm-roadie: update failed: <reason>`.
- `update --help`, `update -h` and `help update` print Roadie's own help (`rhythm roadie update: refresh the signed profile pack`) with `--clear`, `--json`, `--pin` and `--offline`.
- `copy`/`plan --to <name>.sql.bz2` stops while the arguments are read, before the source: `dump-format: --to out.sql.bz2: Roadie reads bzip2 dumps but doesn't write them. Name the copy .sql, .sql.gz or .sql.zst.` (exit 2).
- `explain key.live` explains the check that stands for every `key.<provider>-live`; every check ID verify lists has an explain page.
- `update --clear` prints `Removed the cached profile pack; Roadie uses its built-in profiles until rhythm-roadie update runs again.`
- `version --json`: `{"schema":"rhythm-version/v1","name":"rhythm-roadie","version":"1.0.0","commit":"...","kit_version":"v0.8.0"}`, the fields rhythm's inventory reads; `rhythm help roadie` runs `rhythm-roadie --help` and gets the top help on stdout with exit 0. A test builds the binary and checks both answers against the router's own fixtures.

### For rhythm 0.9.0 (the rhythm repository's PR)

`tools.json` entry, exactly:

```json
{
  "name": "roadie",
  "repo": "RhythmShahriar/roadie",
  "aliases": ["ro"],
  "description": "is my staging a safe copy of production? pseudonymised copies, keys and side effects off",
  "default_install": true,
  "min_rhythm": ""
}
```

`rhythm doctor` checks, shown only when Roadie is installed, each a warning (never a failure, since each is only needed for some runs):

| ID | ok | warn |
|---|---|---|
| `roadie.ssh` | `ssh: <path>` | `ssh isn't on PATH: Roadie needs it for ssh:// sources and targets` |
| `roadie.wp-cli` | `wp: <path>` | `wp isn't on PATH: Roadie needs WP-CLI for @alias sources and targets` |
| `roadie.signing-key` | `Roadie signing key roadie:7Q3kXc2… in <path>` (or `from ROADIE_SIGNING_KEY`) | `no Roadie signing key yet: the first copy, prepare or verify makes one in <path>; set ROADIE_SIGNING_KEY in CI` |

`rhythm lock` records the pack as `profiles=<version>` through `update --json --pin`.

## GitHub Action `RhythmShahriar/roadie@v1`

Composite, branding `shield` in orange. Steps: pick the version (the Action's own tag; a branch or major tag takes the latest release and warns), install cosign, download `rhythm-roadie_<os>_<arch>.tar.gz` (`.zip` on Windows), `checksums.txt` and `checksums.txt.sigstore.json`, check them with `scripts/verify-release.sh` (signature first, for the version's own tag, then the archive's checksum), set up the SSH key, run Roadie, remove the SSH key (always), upload SARIF, upload the reports, comment on the pull request, then fail the job on findings. A later use in the same job reuses the install.

Inputs:

| Input | Default | What |
|---|---|---|
| `mode` | `verify` | `verify`, `copy`, `prepare` or `plan` |
| `source` | | copy and plan: `--from` |
| `target` | | `--to` (copy, plan) or the staging argument (prepare, verify) |
| `url` | | copy, prepare and plan: `--url`; verify: `--site` |
| `production` | | `--production`, comma-separated hosts |
| `confirm-target` | | copy and prepare: `--confirm-target` |
| `config` | | `--config`, relative to `working-directory` |
| `working-directory` | `.` | where every step runs |
| `fail-on` | `high` | `--fail-on`; `none` never fails on findings |
| `baseline` | | verify: `--baseline` |
| `allow-sample` | `"false"` | a sampled verify counts as failed unless `"true"` |
| `sarif` | `"false"` | verify: also write `roadie.sarif` |
| `sarif-upload` | `"false"` | upload it to code scanning (category `roadie`); needs `sarif: "true"` and `security-events: write` |
| `args` | | extra flags, split like a shell (quotes, backslashes) but never run through one: `$VAR`, `$(...)`, `~` and globs stay as written |
| `version` | the Action's tag | `1.0.0` or `latest` |
| `upload` | `"false"` | upload `report.json`, `report.html` and `roadie.sarif` as `roadie-report` (`roadie-report-2` and so on for later uses in a job) |
| `summary-details` | `"false"` | the full Markdown report in the job summary instead of counts |
| `pr-comment` | `"false"` | post the summary as one comment, updated on later runs; needs `pull-requests: write` |
| `ssh-key` | | private key, written to `~/.ssh/roadie_action_key` (0600), `IdentityFile ~/.ssh/roadie_action_key` under `Host *` at the top of `~/.ssh/config`; removed and `~/.ssh` restored when the Action ends |
| `known-hosts` | | required with `ssh-key`, appended to `~/.ssh/known_hosts`; host key checking is never turned off |

Outputs: `exit-code` (0, 1, 2, 3 or 130), `verdict` (`pass`, `fail`, `error`), `findings` (count at or above `fail-on`; `0` with `none`), `report` (path of the signed `report.json`), `html`, `sarif-file`, `signature` (key ID, empty when unsigned), `version`.

Roadie runs as `rhythm-roadie <mode> ... --fail-on <level> --json -o $RUNNER_TEMP/roadie-report/report.json -o .../report.html [-o .../roadie.sarif] <args>`; the JSON on stdout stays out of the log, which shows stderr and the line `Roadie: <WORD>, <counts> (exit <n>)`. The copy of stderr kept for the summary drops `::add-mask::` lines and shows the values they mask as `***`, as the job log does.

Job summary by default (counts only, no hosts, tables, columns or values):

```text
## Roadie: FAIL (verify)

| | |
|---|---|
| Findings | 1 critical · 1 high · 3 medium · 0 low · 2 info |
| Tables checked | 12 of 12 |
| Live keys found | 1 |
| Signed by | `roadie:7Q3kXc2bW9pLmN4a` |

Pseudonymised, not anonymised. ...

_Counts only. Set summary-details: "true" for the full report here, or upload: "true" for the HTML report as an artifact._
```

copy, prepare and plan show `Tables pseudonymised`, `Values pseudonymised`, `Live keys` (`<n> found, <n> swapped, <n> blanked`), `Side effects off` and `Uncovered columns` instead. A run that stopped adds `Roadie stopped:` and its message in a code block; one that wrote no report shows `## Roadie: ERROR` / `Roadie could not run:` with Roadie's last five lines. Text from the run sits in a fence longer than any it contains.

Messages (all `::error::` unless marked, exit 2 for the input errors):

- `Roadie won't copy production in a pull request from a fork: the workflow would need production credentials. Run it on push or schedule; don't use pull_request_target for this.` (copy and prepare on `pull_request` or `pull_request_target` from a fork, a deleted fork or without a readable event payload)
- `::warning::upload is on in a public repository: anyone who can read it can download the roadie-report artifact, which names your tables and columns and shows masked values. Turn upload off or keep the repository private.`
- `::warning::sarif-upload is on in a public repository: its code scanning alerts name your tables and columns. Check who can see them before you keep it on.`
- `Roadie checked a sample of each table, not every row, so this run counts as failed. Drop --sample, or set allow-sample: "true" to accept a sampled verify.` (exit-code 1)
- `the ssh-key input needs known-hosts too: the Action never turns host key checking off. Set known-hosts to the output of ssh-keyscan for your hosts, checked by hand, from a secret.`
- `unknown mode "<m>": use verify, copy, prepare or plan`; `mode copy needs the source and target inputs`; `mode prepare needs the target input: the staging site to make safe`; `mode plan needs the source input (a copy's plan) or the target input (a prepare's plan)`; `the confirm-target input works with mode copy or prepare, not <mode>`; `the baseline input works with mode verify, not <mode>`; `the sarif input works with mode verify, not <mode>`; `the sarif-upload input needs sarif: "true"`; `the <input> input is "<v>": use "true" or "false"`; `the args input has a quote that isn't closed`
- `Roadie found something at or above --fail-on <level>. See the job summary.` (the last step, on exit 1)
- `::warning::Roadie's version isn't pinned: the action ref "<ref>" isn't a release tag such as v1.2.3, so the latest release is installed and a new one can change your results. Use the Action at a release tag or set the version input.`
- `Roadie's release didn't verify: <reason>` (a bad signature, a changed archive, a second checksum line, a missing file)

Example, a nightly refresh (README, "GitHub Action"):

```yaml
- uses: RhythmShahriar/roadie@v1
  with:
    mode: copy
    source: ssh://deploy@shop.com/var/www/shop
    target: ssh://deploy@staging.shop.com/var/www/staging
    url: https://staging.shop.com
    confirm-target: staging.shop.com
    ssh-key: ${{ secrets.DEPLOY_KEY }}
    known-hosts: ${{ secrets.KNOWN_HOSTS }}
    args: --yes
  env:
    ROADIE_SIGNING_KEY: ${{ secrets.ROADIE_SIGNING_KEY }}
```

`action-test.yml` runs the Action from the working tree on Linux, macOS and Windows against the demo shop's dump (`go run ./scripts/demodump -o leaky.sql -leaky`): verify fails with every output set, a copy to a dump passes and verifies. `scripts/action-test.sh` tests the scripts offline.

## README and ROADMAP

README sections: banner, the question and the positioning line, badges, the copy PASS screenshot (`docs/assets/copy.svg`, made from the colour golden by `go run ./scripts/screenshot -readme ...`; a test fails when it is out of date), The Rhythm set (rhythm, Tuner, Soundcheck, Echo, Roadie), Try it in a minute, Safety promises, Copy a site, Make an existing staging safe, Check a staging, What Roadie changes, What Roadie can't see, GitHub Action, Verify a download (the cosign command for `RhythmShahriar/roadie`), Docs (every wiki page of PRD 23), Contributing, Responsible use & disclaimer, License (`Roadie's own code is MIT. The rhythm-roadie binary also includes the rhythm kit, which is AGPL-3.0-only.`; the owner's PRD 24.1 decision may change this sentence). ROADMAP lists PRD 5's "Not in v1" with one line each, plus keyless report signing (PRD 25).
- Report JSON and SARIF pass every string that may come from the data (row keys, paths, table, column and file names, examples, masked keys, titles) through term.SafeText, as the screen, Markdown and HTML do: no control character from a production row key reaches `jq -r`.
- After a prepare, `url.production` and `url.production-siteurl` say `Run prepare again with --url <staging URL> ...` and a prepare run without `--url` that left production's URLs ends with `rhythm-roadie prepare <site> --url <staging URL> rewrites production's URLs`; a failed copy into a site still points at `rhythm-roadie prepare <site> fixes what it can of these`.
- plan lists the dump reader's lines under `DUMP STATEMENTS`, apart from `FILE EDITS` and without the dump's path: the dump itself is never edited.
- The Action maps `unchecked` (and an older report's `pass` with `verify_mode` `none` for a copy or prepare) to `DONE, not checked` in the job summary and `verdict=unchecked`.
