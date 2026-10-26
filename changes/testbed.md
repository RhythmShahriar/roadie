# Testbed, oracle, benchmark and demo data

What the testbed lane added, for the docs team and for anyone who runs the benchmark. Everything here lives under `testbed/`, `internal/demo/`, `docs/benchmark.md` (generated) and `.github/workflows/nightly.yml`.

## Run it

Needs Docker with Compose v2 and Go. From the repository root:

```sh
make -C testbed up          # build, start and seed the sealed testbed (a few minutes the first time)
make -C testbed seal        # TestNetworkIsSealed: no container reaches a public address
make -C testbed bench       # every scenario, the control pass and the other tools
make -C testbed reproduce   # the benchmark twice; fails if the score differs
make -C testbed down        # removes the containers and their volumes; fails if a volume is left
```

`make bench` builds `rhythm-roadie` from the checkout without cgo (one static binary runs on the host and in every container), writes `testbed/bench/work/results.json` and, for a run of every scenario, `docs/benchmark.md`. Options: `ONLY=r01,v01` runs some scenarios (and never replaces the page), `DRILL=5m` sets the side-effect drill's length, `BASELINE=<results.json>` turns on r17's regression guard. Other targets: `make reset ID=r01` (reseed one fixture and load it), `make oracle ID=r01 DUMP=out.sql [TREE=dir]` (judge any output), `make drill [FOR=5m]` (the drill alone on the running staging), `make gen [GEN_SIZE=20GB]` (r17's large dumps), `make formats` (r18's real-tool dumps), `make tools` (fetch the other tools).

The harness itself: `go run -tags testbed ./testbed/bench [-bin <linux binary>] [-only r01,v01] [-tools all|none|wp-cli,replibyte,greenmask,staging-shield] [-doc <path>|none] [-baseline <results.json>] [-drill-for 5m] [-rounds 5] [-seed 1] [-per-run]`, plus `-diff a.json b.json` (compare two scores) and `-render <results.json> [-doc <path>]` (write the page from an earlier run). Exit codes: 0 every scenario caught with no false alarm, 1 something missed, errored, a false alarm or a failed performance check (or `-diff` found a difference), 2 usage, 3 the testbed or the binary couldn't be used (`the testbed isn't running: make -C testbed up`).

Nothing prints a secret. Passwords, Echo's token, the SSH keys, the signing and consistency keys and the fake API keys are made at `make up`, written 0600 under `testbed/runtime/` (gitignored) and passed through the environment only. Canaries appear as IDs (`r04-email-0003`) and keys masked, in results.json, on screen and in the page.

## Ports

- The gate publishes `127.0.0.1:18920-18929` only: 18920 prod-wp, 18921 staging-wp, 18922 Laravel, 18923 Mailpit, 18924 Echo's API, 18925 sshd, 18926 prod-db, 18927 staging-db, 18928 laravel-db, 18929 PostgreSQL; 18930-18939 are reserved and not published.
- PRD 22.1 gives Go test listeners `127.0.0.1:23240-23259`. This lane's tests use the ports it was assigned instead, `127.0.0.1:33120-33139`: 33120 and 33121 the sinkhole, 33122 the stubs, 33123 the tools fetch test, 33124 and 33125 the traffic generator and Echo stand-in. Integration can move them into 23240-23259 (one constant per test file).

## The testbed

A Docker Compose project, `roadie-testbed`, on a network with `internal: true`: `prod-wp` and `staging-wp` (WordPress 6.8.3 with WooCommerce 9.8.5, WP Mail SMTP, WP Mail Logging, FluentSMTP and Subscriptions core), `prod-db` (MySQL 8.4), `staging-db` (MariaDB 11.4), `laravel` and `laravel-db`, `pg` (PostgreSQL 16), `sshd` (WP-CLI and the production site, key login only), `sinkhole` (DNS answers every name with its own address; every TCP port lands on it and is recorded with host, port, SNI and the first 256 bytes, the values of Authorization, Proxy-Authorization, Cookie, Set-Cookie, X-Api-Key and Api-Key headers masked as `[masked]`), `stubs` (Stripe, PayPal, Twilio, Zapier and Mailchimp hosts; a call with a live key is recorded as live), `mailpit` and `echo` (rhythm-echo in seal mode). Databases are on tmpfs. Images are pinned by digest and plugin zips by URL and sha256.

## What each scenario proves

`testbed/scenarios.yaml` lists the 40 scenarios of PRD 22.2. Expectations match finding IDs and error codes, never wording.

- **Copy** (r01 to r12, r15 to r19, r28): a copy of each kind of site leaves no seeded personal value or live key in any encoding, keeps every serialized value and row count, boots, sets off nothing and passes Roadie's own verify. r01 HPOS shop; r02 HPOS compatibility mode; r03 legacy sessions and payment tokens; r04 Action Scheduler args across the 191-byte boundary; r05 Subscriptions with live Stripe IDs and production URL defines; r06 Gravity Forms and an upload (`uploads.personal`); r07 mail logs with reset links; r08 serialized torture with a broken value left untouched; r09 every URL spelling, lookalikes left alone; r10 and r11 multisite (subdirectory with `wp_1_` against `wp_11_`, subdomain with a mapped domain); r12 live keys swapped and test keys byte-identical; r15 Laravel (`data.encrypted`); r16 generic SQL with join keys and a forgotten column (`uncovered`); r17 the large dump (performance); r18 every dump format, imported into MySQL 8.4 and MariaDB 11.4; r19 copying over SSH leaves no raw production data on the local disk (unknown host key exits 3 `ssh-host-key`); r28 PostgreSQL.
- **Prepare** (r13, r14, r21): r13 nothing gets out during prepare (with traffic and a host cron) or the drill after; r14 wp-config traps are replaced in place and `config.points-at-production` is reported; r21 a second prepare is byte-identical and another key re-maps fully.
- **Environment** (r20, r22 to r27): r20 interrupt, SSH cut and disk full leave no finished-looking output; r22 Echo down fails closed; r23 a plugin's every egress path is held; r24 hostile dumps are refused or dropped with the right code; r25 production refusals exit 3 before any write; r26 50 SIGKILLs never leave a state verify passes; r27 plan changes nothing on a SELECT-only user and a snapshot.
- **verify, unsafe** (v01 to v07): verify exits 1 with the expected IDs on a search-replace-only staging, values left in serialized and JSON, PII only in free text, a live key only in a file, an indexable and wired staging, logs with a backup, or hashed emails.
- **verify, clean** (g01 to g05): verify exits 0 with no finding at or above low on clean sites, five rounds each; g05 verifies every copy's output again and checks its report.

## The four gates

Per copy, prepare and environment scenario, in this order with the run checks before them:

1. **Canaries**: the oracle finds none of the scenario's seeded personal values in the output dump or file tree, in any of 22 encodings (raw, lower, upper, four JSON spellings, three URL spellings, three HTML spellings, base64 and base64url at each offset, PHP serialized, md5, sha256), each also in its mysqldump-escaped spelling. Live keys left and test keys changed are gates of their own.
2. **Integrity**: every cell that held valid serialized PHP in the input still unserializes in real PHP (`allowed_classes => false`); the row count of every input table is unchanged (PostgreSQL: rows only).
3. **Side effects**: zero sinkhole connections, zero stub calls with a live key and zero mail captured by Echo to a recipient outside the reserved domains, during the run (with the traffic generator and the host cron where the scenario says so) and during the side-effect drill after it (cron, Action Scheduler, an order, a password reset, a renewal and every webhook, in rounds for 5 minutes). A capture to a fake address is a pass. Echo must be readable: when its captures can't be cleared or read (Echo down, a wrong token) the gate fails with `Echo's captures couldn't be read` in its detail, except in r22, which stops Echo on purpose. The harness checks that Echo answers before it starts (`bench: echo doesn't answer on http://127.0.0.1:18924 (make -C testbed up): ...`, exit 3) and after r22 waits up to 30 s for Echo to come back; if it doesn't, the scenario's run notes say `teardown: echo didn't answer again after the scenario: ...` and every later gate 3 fails.
4. **verify**: a fresh `rhythm-roadie verify` of the result exits 0 with no finding at or above medium; `report check` accepts its report.

The copy must also boot (home, shop, cart and checkout answer 200). A scenario is caught only when every gate holds.

When `rhythm-soundcheck` is on PATH, each boot check also runs `rhythm-soundcheck run http://127.0.0.1:18921/ --quick --modules health --no-download --proxy <seal> --fail-on error` on the same pages (isolated home, `RHYTHM_NO_UPDATE_CHECK=1`, 3 minute limit) and records a run note: `soundcheck: <n> pages healthy`, `soundcheck: issues at error level: ...` or `soundcheck: not run (<reason>)`. Chrome runs on the host, so every request it or Soundcheck makes goes through a seal proxy the harness starts on 127.0.0.1: it forwards the staging site only and answers 403 to every other host (CONNECT included), so a copied page can't reach a real site or the host's DNS. The note then ends with `; <n> request(s) outside the site refused: <hosts>` (at most five, sorted, `and <n> more`; hosts only, never a path). It is not scored: the staging site's own URL (`http://staging-wp`) resolves only inside the sealed network, so a browser on the host can't load its assets.

The oracle reports symbolic links in an output tree under `links` (they aren't followed, so what they point to went unread): the output is then not clean (the oracle exits 1) and the case fails the gate `links` (`<n> link(s) in the output tree not followed: <paths>`). It decompresses gzip, zstd and bzip2 by their magic bytes whatever the file is called, the output dump included, and reads every compressed member of a zip archive the same way; a survivor inside one is found at `file:<archive>!<member>`. A stream that fails to decode part way is an error, never a clean verdict. Links in a site copied out of a container are skipped and named in the run notes (`output tree: <n> link(s) not copied or read: ...`).

`-strict` (on in `make bench` and `make reproduce` through `STRICT ?= -strict`, so in the nightly too; `STRICT=` turns it off) also fails the run (exit 1) when a case stopped at `not-built` or the control pass checked nothing although a good scenario ran, printing `strict: <n> case(s) stopped at not-built` or `strict: the control pass checked nothing`. Scenarios the manifest marks pending don't count. results.json counts the not-built cases in `summary.not_built` and marks each with `not_built: true`.

## The oracle is independent

`testbed/oracle` imports nothing from the Roadie module (`TestOracleImportsNoRoadie` walks every import, tests included). It has its own canary reader, encoders, dump reader (mysqldump, mariadb-dump, phpMyAdmin, pg_dump COPY and INSERT) and matcher; it checks serialized values with real PHP. A bug in Roadie's codecs or dump reader can't make the score look better. It reports canary IDs, encodings and where they were found, never a value.

## The other tools

The benchmark runs other tools on the same inputs after the scenarios and records them in results.json under `tools`; each is judged only on what it does; anything else is "can't see". Configuration is in `testbed/tools/`, versions in `testbed/Makefile`.

- WP-CLI `search-replace` 2.12.0 on r01, r09 and r12: production URLs left, plus lookalike links, other subdomains and addresses at the production domain it changed.
- Replibyte 0.10.0 on r01 and r12: personal values and live keys left, serialized integrity. It runs with telemetry off (`-n`) on the input rewritten as one INSERT per row with named columns (`mysqldump --complete-insert --skip-extended-insert` style): on mysqldump's default style 0.10.0 panics (`Column names do not match values`) or keeps only the first row of each INSERT.
- Greenmask 0.2.26: not run, with the reason "Greenmask 0.2.26 dumps and restores PostgreSQL only; these inputs are MySQL dumps of WordPress shops".
- Staging Shield 1.0.2 on r01 and r13: side effects during the drill on a raw copy with the plugin active.

Both releases are pinned in `testbed/Makefile`: Replibyte by the sha256 its release publishes, Staging Shield by that of the wordpress.org zip (wordpress.org publishes none).

A tool that can't run is "not run" with the reason, never an error.

## Measured so far

- Seeding: 9,149 canaries and keys across the fixtures (r01 1,715; r10 885; r13 486; r06 481; and so on); every MySQL dump imports into MySQL 8.4 and r28 into PostgreSQL 16.
- The oracle's matcher: about 170 MB/s with 2,000 canaries.
- The seal: all 14 containers pass `TestNetworkIsSealed`; on a raw staging the drill reaches the sinkhole (update checks to api.wordpress.org and woocommerce.com), so gate 3 is not blind.
- r17's generator: 2,000,000,255 bytes of SQL for `2GB`; every sampled serialized cell passes real PHP.
- Replibyte 0.10.0 with the committed configuration, outside the benchmark (same binary, configuration, rewritten input and oracle; no network): r01 left 1,050 of 1,710 personal values and 3 of 3 live keys, broke 0 of 342 serialized values and kept every row; r12 left 10 of 14 live keys and broke 0 of 6 serialized values.
- WP-CLI search-replace with the committed recipe: r01 0 of 303 production URLs left; r09 1 of 274 left (`https://www.shop.example.com/about/`) and 60 of 181 lookalike links changed (the `//shop.example.com` pair rewrites `//shop.example.com.au`); r12 0 of 2 left.
- Roadie's own score: not yet. This lane's checkout has only the CLI stubs, so a run here scores the scenarios missed (or pending where the binary says `not-built`). The first full run at integration fills `docs/benchmark.md`.

## docs/benchmark.md

Generated by the harness, never edited by hand. First line: `<!-- Generated by go run -tags testbed ./testbed/bench. Don't edit by hand: rerun the benchmark. -->`. Sections: what one run does, the honest framing paragraph of PRD 22.3 word for word, the headline (`Roadie left <n> of <N> seeded personal values and <n> of <M> live keys; verify caught <n> of <7> unsafe stagings with <n> false alarms in <K> checks of clean ones.`), run metadata (date, Roadie version, OS and arch, CPU and cores, RAM, disk type, Docker, Go, wall time), tables (scenarios with their gates, canary survival by encoding, side effects, verify recall and false alarms, performance, other tools), "What this does not show", details per scenario and "Run it".

## Nightly (`.github/workflows/nightly.yml`)

Every day at 03:17 UTC and on demand. Results are artifacts; nothing is committed.

| Job | What it runs | A failure means |
|---|---|---|
| `fuzz` | every `FUZZ_TARGETS` entry for 5 minutes, then the testbed's own targets | a fuzz target found an input that breaks it; the input is uploaded (`fuzz-failures`) to commit with a named unit test |
| `php-oracle-corpus` | the PHP serialized codec's committed corpus through real PHP 8.3, then 5 minutes of `FuzzDifferentialPHP` | Roadie wrote a serialized value real PHP can't read, or reads one differently |
| `docker-tests` | `go test -tags docker -timeout 40m ./...` | a test against a real database or site in Docker failed |
| `testbed` | `make testbed`, `make -C testbed seal`, `make -C testbed bench DRILL=5m`, `make -C testbed reproduce DRILL=30s`; uploads results.json and docs/benchmark.md (`benchmark`) | the seal broke, a scenario missed or errored, a clean input raised a false alarm, Roadie answered `not-built` or the control pass checked nothing (`-strict`), or two runs scored differently |
| `large-dump` | r17 at 20 GB (`make -C testbed up GEN_SIZE=20GB`, `make -C testbed bench ONLY=r17`) against the last green night's results (`r17-results`) | r17 missed a gate, peak RSS grew more than 1.5x from the 200 MB run, or throughput dropped more than 20% |

## Fixture templates

Files named `*.php.tmpl` under `testbed/fixtures/` (r14's `wp-config.php.tmpl`) are Go templates: `php -l` on the raw template fails on the `{{ }}` braces by design. Lint the rendered copy under `testbed/runtime/fixtures/<id>/files/` after `make -C testbed up` (or `go run ./testbed/seed`), which passes.
