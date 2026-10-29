// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"strings"
)

// The help texts are written out in full rather than built by the flag
// package, so they read exactly as PRD 6.1 to 6.10 give them: grouped,
// with the defaults in words. A table test (flags_test.go) keeps every
// registered flag named in its command's help and the other way round.

// topHelp is `rhythm-roadie help` (PRD 6.1). %s is the version.
const topHelp = `♪ rhythm roadie  %s
Is my staging a safe copy of production? Make pseudonymised staging copies with
live keys swapped and side effects off, then prove it with a signed report.

Usage:
  rhythm-roadie                                 pick what to do (on a terminal)
  rhythm-roadie copy --from <source> --to <target> [flags]
                                                copy production to staging: pseudonymise,
                                                swap keys, turn side effects off, verify
  rhythm-roadie prepare <staging> [flags]       make an existing staging copy safe in place
  rhythm-roadie verify [staging] [flags]        look for real people, live keys and
                                                production URLs; signed pass/fail report
  rhythm-roadie plan --from <source> [--to <target>]
  rhythm-roadie plan <staging>                  dry run: every change copy or prepare would make
  rhythm-roadie config init [--stack s]         write .roadie/config.yaml to edit
  rhythm-roadie config check | show             validate the config / print it with defaults filled in
  rhythm-roadie config key show|rotate|forget   the key that keeps fake values the same across refreshes
  rhythm-roadie report <report.json>... -o <file>
                                                render saved reports as HTML, Markdown, SARIF or JUnit
  rhythm-roadie report check <report.json>      check a report's signature
  rhythm-roadie profiles [name] [--json]        list the stack profiles and what each rule changes
  rhythm-roadie explain <id>                    a rule's or finding's full explanation, offline
  rhythm-roadie update [--clear]                refresh the signed profile pack
  rhythm-roadie demo [--keep]                   try the whole loop on a built-in shop
  rhythm-roadie help [command] | version        this help, one command's, or the version

` + exitHelp

// exitHelp is the exit code block under the top help and every command's.
const exitHelp = `Exit codes (as in Tuner, Soundcheck and Echo):
  0    ok: done and verify found nothing at or above --fail-on
  1    findings: verify found something at or above --fail-on, a copy stopped on
       uncovered columns, plan --fail-on would stop, or report check found an
       edited report, another input or another key
  2    usage error: a wrong command line, a bad .roadie/config.yaml, a signing
       key that can't be read, or a target to confirm with no terminal
       (pass --confirm-target)
  3    couldn't run: source or target unreachable, a dump that ends early, a
       target refused as production, nothing to scan, an incomplete verify,
       an unsigned report (report check)
  130  interrupted
`

// outputHelp and settingsHelp are the global flags of PRD 6.3, shown
// under every command's own flags.
const outputHelp = `Output:
      --json                 JSON (schema rhythm-roadie/v1)
      --md                   Markdown for pull requests and CI summaries
      --html                 standalone HTML report
      --sarif                SARIF 2.1.0 (verify only)
      --junit                JUnit XML for CI test views (verify only)
  -o, --output <file>        write the report to a file (format from the extension:
                             .json .md .html .sarif .xml), keep the live view;
                             repeat it for several files. With a file of the same
                             format, --json, --md and the others write only the file
      --open                 open the HTML report when done
  -q, --quiet                results only
      --plain                no color, boxes or glyphs (screen readers, logs)
      --no-color             no color (NO_COLOR too)
  -v, --verbose              every table, rule and file edit as it happens
      --debug                timings and counts too (never values)
`

const settingsHelp = `Settings:
      --config <file>        settings file (default the nearest .roadie/config.yaml, here or above)
      --no-config            don't read .roadie/config.yaml; use it in folders you don't trust
      --offline              built-in profiles only, no update check (RHYTHM_OFFLINE=1 too)
`

// The blocks copy and prepare share (PRD 6.4 and 6.5). --max-output-ratio
// (PRD 10.2, 17), --key and --sign=off (lead's decision: on copy, prepare
// and verify alike) are added in the same style.
const pseudonymiseHelp = `Pseudonymise:
      --keep-users <list>    user logins or emails kept as they are, globs allowed,
                             e.g. admin,*@agency.example (default keep_users: in the config)
      --keep-key             keep the consistency key in .roadie/key, so the next refresh
                             gives each real person the same fake (default: a new key per run)
      --email-domain <d>     reserved domain for fake emails (default example.invalid)
      --admin-password-env <NAME>
                             set every kept user's password from this ROADIE_ variable
                             (default: kept users keep their password hash)
      --allow-uncovered      copy even when columns that look personal have no rule
                             (they are listed as findings; default: stop before writing)
      --drop <tables>        leave these tables out of the copy, globs allowed
      --truncate <tables>    copy the table's structure but not its rows
      --one-pass             skip the name index (faster). Site URLs, form fields and
                             every rule still apply. Names in free text may stay; the
                             report says so (info.one-pass)
      --keep-routines        keep triggers, procedures and views (DEFINER removed)
      --keep-app-key         Laravel: keep APP_KEY (encrypted columns stay readable)
`

const keysHelp = `Keys and side effects:
      --keys <mode>          swap (default): a live key becomes the test key you set in
                             keys.test_keys, else it is blanked. blank: blank every live key
      --no-kill-switch       copy the data only (verify will FAIL on side effects)
      --echo-url <url>       where the installed Echo connector reports (default
                             http://127.0.0.1:8025; RHYTHM_ECHO_URL)
      --echo-proxy <addr>    also route raw cURL through Echo's proxy, e.g. 127.0.0.1:8026
      --noindex=off          don't add noindex, robots and the crawler block (default on)
      --basic-auth <user>    also write basic-auth snippets with a generated password,
                             printed once (default off; on GitHub Actions it is
                             masked and kept in .roadie/basic-auth/password). The
                             user is 1 to 64 letters, digits or . _ - @
`

const safetyHelp = `Target safety:
      --production <list>    production hosts or URLs, e.g. shop.com,*.shop.com (adds to
                             production: in the config); a target that matches is refused
      --confirm-target <host>
                             the target's host or database name (a SQLite file's path or
                             name), typed out; needed when the target is a site or
                             database and there's no terminal
      --target-is-staging <host>
                             the target says it's production (WP_ENVIRONMENT_TYPE or APP_ENV)
                             but isn't: name its host to go on. Never overrides a production:
                             match or a target that is the source
  -y, --yes                  don't ask before replacing the target's database
      --fix-collation        rewrite MySQL 8 collations (utf8mb4_0900_*) for older MariaDB
      --jobs <n>             transform workers (default the number of CPUs, at most 8)
      --max-memory <size>    soft memory limit (default 300MiB, or GOMEMLIMIT)
      --max-value <size>     largest single value accepted (default 256MiB)
      --max-output-ratio <n> stop when the dump grows past n times its compressed size
                             (default 20)
`

const afterHelp = `After the copy:
      --no-verify            skip the verify at the end (exit 0 means "copied", not "checked")
      --fail-on <level>      exit 1 when verify finds something at or above none, info, low,
                             medium, high (default) or critical
      --soundcheck           also run rhythm-soundcheck run <url> --json on the copy
      --sign=off             don't sign the JSON report (default: sign it)
      --key <file>           sign with this private Ed25519 key file instead
                             (ROADIE_SIGNING_KEY in CI); report check --key is the
                             public key or key ID to check against
`

// copyHelp is PRD 6.4 with --dry-run added under Where.
const copyHelp = `rhythm roadie copy: copy production to staging, pseudonymised, keys swapped, side effects off

Usage:
  rhythm-roadie copy --from <source> --to <target> [flags]

Where:
      --from <source>        dump file, -, site path, ssh://user@host/path, @alias or DSN
                             (default source: in the config)
      --to <target>          dump file, -, site path, ssh://..., @alias or DSN
                             (default target: in the config)
      --url <url>            the staging site's URL (default url: in the config; asked on a
                             terminal when neither is set; not needed with stack sql).
                             Production's URL is read from the source (siteurl, home, APP_URL)
      --url-map <a=b,...>    extra URL pairs, e.g. for multisite subsites or a CDN:
                             fr.shop.com=fr.staging.shop.test,cdn.shop.com=cdn.staging.shop.test
      --stack <name>         wordpress, laravel or sql (default: detected)
      --site <id>            multisite: only these blog IDs (repeatable; default all)
      --schema <file>        CREATE TABLE statements for a data-only dump
      --dry-run              run plan with the same arguments: every change, none made

` + pseudonymiseHelp + "\n" + keysHelp + "\n" + safetyHelp + "\n" + afterHelp

// prepareHelp is PRD 6.5: the copy blocks plus prepare's own flags.
const prepareHelp = `rhythm roadie prepare: make an existing staging copy safe in place

Usage:
  rhythm-roadie prepare <staging> [flags]
  (staging: a site path, ssh://..., @alias or DSN; default target: in the config)

Where:
      --url <url>            the staging site's URL (default url: in the config)
      --url-map <a=b,...>    extra URL pairs, e.g. for multisite subsites or a CDN
      --stack <name>         wordpress, laravel or sql (default: detected)
      --site <id>            multisite: only these blog IDs (repeatable; default all)
      --only <parts>         data, keys, side-effects, urls, noindex (default all). E.g.
                             --only side-effects re-applies the kill switch after a plugin
                             update without touching the data
      --production-url <url> the live site's URL, for rewriting and for verify (default
                             production: in the config; asked on a terminal when the copy
                             still holds it as siteurl)
      --no-backup            don't back up the database first (default: back it up)
      --dry-run              run plan with the same arguments: every change, none made

` + pseudonymiseHelp + "\n" + keysHelp + "\n" + safetyHelp + "\n" + afterHelp

// planHelp is PRD 6.7 written in the same style.
const planHelp = `rhythm roadie plan: dry run of copy or prepare; lists every change and makes none

Usage:
  rhythm-roadie plan --from <source> [--to <target>] [flags]
  rhythm-roadie plan <staging> [flags]

plan takes every flag of copy (rhythm-roadie help copy) or, with <staging>, of
prepare (rhythm-roadie help prepare). It opens the source and the target
read-only, lists every table, rule, key, file edit, remote command and side
effect and writes only its report.

Plan:
      --schema-only          read only CREATE TABLE statements or information_schema,
                             not the rows (faster; row counts are estimates)
      --fail-on <level>      exit 1 when the copy would stop or leave a finding at or
                             above none (default), info, low, medium, high or critical
`

// verifyHelp is PRD 6.6.
const verifyHelp = `rhythm roadie verify: look for real people, live keys and production URLs in a staging copy

Usage:
  rhythm-roadie verify [staging] [flags]
  (staging: a site path, ssh://..., @alias, DSN or dump file; default target: in the config)

What to check:
      --production <list>    production hosts or URLs, e.g. shop.com,*.shop.com (adds to
                             production: in the config)
      --from <source>        production's dump or database: read it to check for every real
                             value exactly (exact mode) instead of by shape
      --allow-emails <globs> addresses that may stay real, e.g. *@agency.example
      --allow-phones <globs> numbers that may stay real
      --site <url>           also fetch the staging site's /robots.txt and home page headers
                             (never a production host) to check noindex and basic auth; on
                             WordPress, also ask the Echo connector's ping route whether it
                             seals mail
      --insecure-tls <host>  accept a self-signed certificate on this one staging host
      --echo[=<url>]         also ask Echo whether its seal is on and how many messages it
                             let through since the copy (default http://127.0.0.1:8025;
                             RHYTHM_ECHO_TOKEN for an Echo beyond loopback)
      --sample <n>           rows checked per table (default 0: every row)
      --skip <checks>        leave these checks out: emails, phones, names, addresses, ips,
                             cards, keys, urls, side-effects, config, uploads
      --max-value <size>     largest single value read from a dump (default 256MiB)
      --max-output-ratio <n> stop when a dump grows past n times its compressed size
                             (default 20); both apply to --from too

CI:
      --fail-on <level>      exit 1 at or above none, info, low, medium, high (default) or
                             critical
      --baseline <file>      hide findings already in this JSON report (accepted test data)
      --sign=off             don't sign the JSON report (default: sign it)
      --key <file>           sign with this private Ed25519 key file instead
                             (ROADIE_SIGNING_KEY in CI); report check --key is the
                             public key or key ID to check against
`

// configHelp is PRD 6.8 written in the same style.
const configHelp = `rhythm roadie config: write, check and print .roadie/config.yaml and its key

Usage:
  rhythm-roadie config init [--stack wordpress|woocommerce|laravel|sql] [--from <source>] [--force]
  rhythm-roadie config check [--json]
  rhythm-roadie config show [--json]
  rhythm-roadie config key show|rotate|forget [--force]

  init     write .roadie/config.yaml with every key commented
  check    parse and validate it, list the ${ROADIE_*} variables that are unset
           and any database password written into source: or target:
  show     the effective config with defaults, where each value comes from
           (production: of .soundcheck/ and .echo/ too) and the rules expanded:
           columns: and each profile it may apply; secrets masked
  key      the consistency key's fingerprint, a new key, or delete .roadie/key
           (ROADIE_KEY wins over the file)

Config:
      --stack <name>         init: wordpress (default), woocommerce, laravel or sql
      --from <source>        init: read this source's schema (read-only) and list its
                             unknown tables with personal-looking columns as suggestions
      --force                init: replace an existing .roadie/config.yaml;
                             key rotate: replace the key without a terminal
`

// reportHelp is PRD 6.9 written in the same style.
const reportHelp = `rhythm roadie report: render saved reports, or check a report's signature

Usage:
  rhythm-roadie report <report.json>... -o <file> [--brand-name <name>] [--brand-logo <file>]
  rhythm-roadie report check <report.json> [--key <file|id>] [--input <dump>]

  Several reports make one summary page, worst first. The format comes from
  the -o file's extension: .html .md .sarif .xml

Report:
      --brand-name <name>    a name for the top of the HTML report, e.g. your agency's
      --brand-logo <file>    a PNG, SVG or JPEG logo for the HTML report (under 512 KiB)
      --key <file|id>        check: the public key file or key ID (roadie:..., which
                             report check prints) the report must be signed with;
                             copy and verify --key is the private key that signs
      --input <dump>         check: the dump the report describes; its fingerprint must match
`

// profilesHelp, explainHelp and demoHelp are PRD 6.10 written in the same style.
const profilesHelp = `rhythm roadie profiles: list the stack profiles and what each rule changes

Usage:
  rhythm-roadie profiles [name] [--json]

  Without a name: every profile with its ID, name and what it was checked
  against. With a name: that profile's detector, tables, columns, rules and
  kept fields.
`

const explainHelp = `rhythm roadie explain: a finding's, rule's or error code's full explanation, offline

Usage:
  rhythm-roadie explain <id>

  e.g. rhythm-roadie explain key.stripe-live, explain side-effect.action-scheduler,
  explain uncovered-column, explain pseudonymised
`

const demoHelp = `rhythm roadie demo: try the whole loop on a built-in shop, offline

Usage:
  rhythm-roadie demo [--keep]

  Builds a small WooCommerce dump with fake "real" customers, copies it to a
  temporary dump file, verifies the copy (PASS), then verifies the untouched
  input (FAIL). No network, no database server.

Demo:
      --keep                 keep the demo's dumps and reports (default: delete them)
`

// updateHelp is update's own help: kit runs the command, so its generic
// text would leave out --clear.
const updateHelp = `rhythm roadie update: refresh the signed profile pack

Usage:
  rhythm-roadie update [--clear] [--json] [--pin profiles=<version>]

  Downloads the latest signed profile pack, checks its signature and keeps it
  in the cache; copy, prepare, plan and verify use it when it is newer than
  the built-in one. Offline (--offline or RHYTHM_OFFLINE=1) it downloads
  nothing and reports the pack in use.

Update:
      --clear                drop the downloaded pack and use the built-in one
      --json                 print the rhythm-data/v1 JSON document
      --pin <name=version>   download these versions instead of the latest,
                             as name=version,... (profiles=<version>)
      --offline              download nothing; report the pack in use
`

// commandHelp maps each command to its own help block.
var commandHelp = map[string]string{
	"copy": copyHelp, "prepare": prepareHelp, "plan": planHelp, "verify": verifyHelp,
	"config": configHelp, "report": reportHelp, "profiles": profilesHelp,
	"explain": explainHelp, "demo": demoHelp,
}

// helpFor returns a command's whole help: its block, then the shared
// Output and Settings blocks and the exit codes.
func helpFor(cmd string) string {
	return strings.Join([]string{commandHelp[cmd], outputHelp, settingsHelp, exitHelp}, "\n")
}

// updateHelpText is update's whole help: its block and the exit codes.
func updateHelpText() string {
	return updateHelp + "\n" + exitHelp
}

// printTopHelp writes the top help with the version.
func printTopHelp(w io.Writer) {
	fmt.Fprintf(w, topHelp, displayVersion())
}

// displayVersion is the version for banners: "dev" for a local build.
func displayVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}
