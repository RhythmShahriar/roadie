# Lane detect: user-visible changes

This lane builds the PII detectors (`detect/`), the live key rules and catalogs (`keys/`) and `verify` (`verify/`). The docs team writes the detector, verify and live key pages from this file.

## Finding IDs verify reports

Every ID is a constant in `core/ids.go`. Severities are fixed unless the row says otherwise. "Guards" are what keeps the finding quiet on data that only looks personal.

| ID | Severity | Flags | Guards |
|---|---|---|---|
| `exact.survivors` | critical | exact mode only: a production value Roadie replaced, found anywhere in the copy as an email, the whole value (up to 512 bytes), a word or two-word pair, a phone digit run (with and without `+`), an md5 or sha256 hex run of the normalised value or inside a base64 run of 12 or more characters (two levels deep); read as written, with HTML entities decoded and with percent escapes decoded. Detail `found as <raw, lower, md5, sha256, base64, url or html-entity>` | values under 4 bytes; Roadie fakes (`pseudo.IsFake`); kept users; never two hits over the same bytes |
| `pii.email` | high | an address anywhere in a value (Unicode local part, a 2 to 24 letter TLD, IDN and `xn--`), also written `&#64;`, `&#x40;` or `%40`, inside JSON and serialized text | reserved domains (`core.ReservedEmailDomain`); `allow_emails` globs; kept users; `wordpress@` at any domain; TLDs that are file extensions (`png jpg jpeg gif svg webp css js map woff woff2 ttf`); `git@host:`; a local part right after `//` (URL user part); plugin update caches in options (see below) |
| `pii.email-role` | info | `noreply`, `no-reply`, `donotreply`, `do-not-reply`, `mailer-daemon`, `postmaster` mailboxes | as `pii.email` |
| `pii.phone` | high | in phone columns (rule kind `phone`, or a name part containing `phone` or equal to `tel tele telefon telefono telephone mobile mobil cell cellular fax`) any 7 to 15 digits; elsewhere only `+` and 8 to 15 digits, `tel:` links or national formats with separators for US/CA, GB, DE, FR, ES, IT, NL, AU, IN, BR, IE, NZ. Arabic-Indic, Extended Arabic-Indic, Devanagari, Bengali and fullwidth digits are read too | fictional ranges (`core.FictionalPhone`: NANP 555-01xx, UK 07700 900xxx, German drama numbers; also Ofcom's UK drama ranges: 0113 to 0118, 0121, 0131, 0141, 0151, 0161 496 0xxx, 0191 498 0xxx, 020 7946 0xxx, 029 2018 0xxx, 028 9649 6xxx, 01632 960xxx and 08081 570xxx); `allow_phones` globs; name parts `sku ean upc isbn gtin tracking total price amount order id timestamp postcode zip`; a currency sign or decimal part next to the number; UUIDs and 32+ hex runs; codes glued by `- _ / #`; ten-digit Unix times outside phone columns; versions and dotted quads |
| `pii.name` | medium | a value in a name column (`first_name`, `last_name`, `*_first_name`, `*_last_name`, `display_name`, `comment_author`, `author`, `full_name`, `name` in a people table, or a rule of a name kind) that is not a Roadie fake | the row's email is reserved or a Roadie fake; the row's email is on `allow_emails` (the business's own account); kept users; the software authors `WooCommerce`, `WordPress`, `Action Scheduler`, `ActionScheduler`, `System`, `WP-CLI`, `Cron` (any case); single characters; values with `@` or `://` or no letter |
| `pii.address` | medium | a value in a street column (`address_1`, `address_2`, `street`, `address`, `billing_address_1`, `shipping_address_1`, any `*_<those>`, or a street rule) that is not a Roadie street | the row guards of names; `*_address` names holding `ip email mail mac url web wallet server remote`; in a settings table (`*options`, `*sitemeta`, `*settings`) a name holding `store company business shop merchant seller` (`woocommerce_store_address` is the shop's own) |
| `pii.ip` | medium | public IPv4 and IPv6 in IP columns (`ip`, `ip_address`, `*_ip`, `comment_author_IP`, `_customer_ip_address`, `remote_addr`) and under `ip ip_address ipaddress user_ip remote_addr client_ip` keys inside serialized and JSON values | loopback, private, link-local, multicast, CGNAT, 0/8, 192.0.0/24, 198.18/15, 240/4, documentation ranges (RFC 5737, 2001:db8::/32); dotted numbers in free text |
| `pii.card` | high | 13 to 19 digits starting 2 to 6, Luhn-valid, grouped like a card (fours, Amex 4-6-5, Diners 4-6-4) or any digit run in a card column (`*card*`, `pan`, `cc`) | the published Stripe and Braintree test cards; ungrouped digits in free text; mixed separators |
| `pii.iban` | medium | an IBAN with its country's registry length and a valid mod-97 checksum | the documented example IBANs; wrong length or check digits (IBAN-shaped order references) |
| `data.encrypted` | info | a Laravel Crypt payload (base64 of JSON with iv, value, mac and an optional tag), as a whole value or a leaf inside JSON or serialized data (a `ShouldBeEncrypted` job's `data.command` in `jobs.payload`) | none |
| `uncovered-column` | medium | a text column no rule covers whose name has a PRD 11.3 part or whose first 1,000 non-empty values are 20% or more real-looking emails or strong-format phones | rule-covered columns; a name ending in a word about the value (`type kind status format verified at id ids count enabled sent opened confirmed optin optout preference preferences flag template subject provider mailer driver host port encryption`: `address_type`, `email_verified_at`, `MAIL_HOST`); the content sample still applies to those |
| `key.<provider>-live` | per provider (table below); low for a disabled gateway | a live key | test keys; placeholders; masked values; a hit inside a short value that is a placeholder as a whole; a key whose characters after the marker are a stand-in anywhere in a value (`sk_live_xxxx...` in help text or a code comment) |
| `key.unknown-env` | medium | a provider key Roadie can't tell live from test | none |
| `key.live-mode` | high | a catalog gateway enabled and in live mode, even with blank secrets. Title `<Gateway name> is in live mode` | disabled gateways; WooPayments when wp-config.php puts it in dev (sandbox) mode: `WCPAY_DEV_MODE` true, `WP_ENVIRONMENT_TYPE` `development` or `staging`, or any `WP_DEVELOPMENT_MODE` |
| `key.generic` | high | kit's secret rules on a value, or on `name="value"` built from a secret-looking setting name | the site's own settings (`DB_PASSWORD DB_USER DB_USERNAME DB_NAME DB_HOST DB_DATABASE DATABASE_PASSWORD REDIS_PASSWORD WP_REDIS_PASSWORD MEMCACHED_PASSWORD WP_CACHE_KEY_SALT APP_KEY` and the eight salts); names that look secret but aren't: `_order_key order_key _edit_lock session_key _wp_session_key meta_key option_key _transient_key session_tokens`; kit's name = value rules never run on whole posts or comments |
| `url.production` | medium | a production host in a text value (codec/urlrw forms) | lookalike hosts; email domains; other sites |
| `url.production-siteurl` | critical | `siteurl` or `home` in any `*options` table, or `WP_HOME`, `WP_SITEURL`, `APP_URL` in the config files, still on production. Title `<NAME> is still production's` for config | none |
| `config.points-at-production` | critical | `DB_HOST WP_REDIS_HOST AS3CF_SETTINGS REDIS_HOST MEMCACHED_HOST AWS_BUCKET AWS_ACCESS_KEY_ID` equal to production's or on a production host; `AS3CF_SETTINGS` also by its bucket or keys. Title `<NAME> points at production` | text comparison only, no DNS; a server setting that is machine-local (localhost, a loopback, unspecified or link-local address, a socket path or a one-label name such as `db`) is not compared with production's text, only with the production hosts |
| `config.salts` | medium | exact mode: the eight salts or `APP_KEY` equal to production's | none |
| `side-effect.mail` | critical | `--echo`: Echo unreachable, not in seal mode or let messages through | none |
| `search.indexable` | medium | `--site`: no `noindex` header or meta tag, or robots.txt lets `*` crawl | a robots.txt group for `*` with `Allow: /` beside `Disallow: /` lets crawlers in (RFC 9309); meta robots content is read as a comma list (`index, nonexistent` holds no noindex) |
| `auth.none` | low | `--site`: the home page answers 2xx without a password | a 401 with `WWW-Authenticate` |
| `uploads.personal` | backups and dumps critical, logs high, form uploads medium | files over 0 bytes at `wp-content/debug.log`, `wp-content/uploads/wc-logs/`, `wp-content/uploads/gravity_forms/`, `wp-content/updraft/`, `wp-content/ai1wm-backups/`; `*.sql`, `*.sql.gz`, `*.zip` in the web root or `wp-content/`. Names and sizes only | `index.php`, `index.html`, `.htaccess`; empty files |
| `soundcheck.failed` | medium | `verify.SoundcheckFinding` for a Soundcheck exit 1 | Soundcheck not installed is a note, never a finding |
| `info.ignore-expired` | info | an ignore entry past its `until` date | none |

The side-effect checks of the site lane (`side-effect.cron` and the rest) run through `Job.Steps`; verify records their side effects and findings as they come.

**Plugin update caches.** The PII detectors skip values of options and network options that cache public plugin and wordpress.org data: names starting `_site_transient_update_`, `_site_transient_theme_roots`, `_site_transient_wporg_`, `_site_transient_poptags_`, `_site_transient_browser_`, `_site_transient_php_check_`, `_site_transient_feed_`, `_transient_feed_`, `_transient_plugin_slugs`, `_transient_wporg_`, `_transient_dash_`, `_site_transient_community-events-`, `_transient_timeout_`, `_site_transient_timeout_`; also the whole names `active_plugins`, `recently_activated`, `uninstall_plugins`, `active_sitewide_plugins`, `auto_update_plugins`, `auto_update_themes`. Plugin headers there hold vendor emails. The survivors, key and URL checks still read them.

## Masking

Every example passes through `core.Mask*`; no flag prints a raw value.

- email `ad•••@gm•••.com`; phone `+44 •••` (a national number without `+`: `•••`); IP `81.•••`; name and street `A•••`
- card and IBAN by `core.MaskKey` (`4539•••••`, `GB29•••••`); keys by `core.MaskKey` (`sk_l•••••`)
- survivors by the value's kind; md5 and sha256 hits `•••`
- row locations hold only the primary key and `option_name`/`meta_key` (`core.MaskRow`); a key value with `@` is masked

## Verify modes, sampling and coverage

- `exact` when the run has the set of replaced values (`Job.Known`), else `heuristic`; `exact.survivors` is skipped in heuristic mode.
- `--sample N` scans the first N rows of each table and sets `coverage.sample`. Every row otherwise.
- `--jobs` workers (default GOMAXPROCS); results merge in input order, so examples and locations are the same on every run.
- Findings group by ID and table and column (or file), at most 3 examples and locations each.
- A table that can't be read is listed under coverage skipped with its reason. When the input fails part way, the entry is `(input after <last table read>)` with the reason plus `; the tables after it weren't read`; before any table it is `(input)` with the reason. Zero rows read: exit 3 `nothing-scanned: verify read 0 rows from <target> (<why>). A verify that sees nothing can't pass; check the user's SELECT rights.`
- Code files (local sites only): `.php` under `wp-content/themes` and `wp-content/plugins`, depth 6, up to 2 MiB, live provider keys only, action `manual`, fix text PRD 17's key-manual sentence. Over ssh: coverage skipped `code files`, `not read over ssh (PRD 6.13 allowlist)`. A folder that can't be listed adds `code files`, `<folder> not listed: <error>`; unreadable files add `code files`, `<N> file(s) in <folder> not read; first <path>: <error>`. A missing folder adds nothing.
- `--skip`: `emails`, `phones`, `names`, `addresses`, `ips`, `cards` (cards and IBANs), `keys`, `urls`, `side-effects`, `config`, `uploads`.

## Checks

`VerifyResult.Checks` has one entry per check, sorted by ID: `auth.none`, `config.points-at-production`, `config.salts`, `data.encrypted`, `exact.survivors`, `key.generic`, `key.live` (every `key.<provider>-live`; new exported `verify.CheckKeyLive`), `key.live-mode`, `key.unknown-env`, `pii.address`, `pii.card`, `pii.email`, `pii.iban`, `pii.ip`, `pii.name`, `pii.phone`, `search.indexable`, `uncovered-column`, `uploads.personal`, `url.production`, `url.production-siteurl`, each Step's ID and `side-effect.mail` with `--echo`.

- `fail` when a finding of the check is at or above `--fail-on` (low when `--fail-on` is none); detail `<n> hit` or `<n> hits`. `pass` when it ran without one. Hidden findings never fail a check.
- `skip` with the reason as detail: `left out with --skip <name>`; `heuristic mode: no record of what Roadie replaced`; `no production hosts to look for; set production: in .roadie/config.yaml`; `production: lists only globs, which the URL matcher can't search for`; `a database or dump target has no files to read`; `production's wp-config.php or .env wasn't read in this run`; `--site not given`; `the home page couldn't be fetched`; otherwise `nothing to check on this target`.
- `checked`: rows scanned for the data checks (plus config and code files for the key checks), tables for `uncovered-column`, folders listed for `uploads.personal`, pages fetched for `--site` checks, 1 per Step and per Echo call.
- The verdict comes from `core.ExitFor` over the findings, as before.

## --baseline and ignore entries

- `--baseline <report.json>` reads a report with schema `rhythm-roadie/v1` (any kind; the signature is not checked) and hides every finding whose ID plus table and column (or file) appear among the baseline's findings and locations. Errors, all exit 2 code `config`: `--baseline <path>: no such file; give a report.json from .roadie/reports or a --json run`, `--baseline <path>: isn't a Roadie JSON report: <error>`, `--baseline <path>: schema is "<s>", not rhythm-roadie/v1`, `--baseline <path>: larger than 64 MiB, which no report is`.
- `ignore` entries (`id`, `location`, `reason`, optional `until`) hide matching hits while in force. `until` is a date: the entry hides through that whole UTC day. A location may be written as the report shows it (`wp_options 12 shop_phone`, `wp_users.user_email`, `wp-config.php line 41`, with or without ` → <path>`) or shorter: the table and one row key value (`wp_options shop_phone`), the table and `key=value`, the table and column or the file alone. `*` matches like a shell glob (`wp_options shop_*`). An entry for one row hides that row only; the rest of the column is still reported.
- An entry missing id, location or reason is exit 2 `verify ignore entry <n> needs id, location and reason, e.g. {id: pii.phone, location: "wp_options shop_phone", reason: "the shop's own number"}`; an unknown ID is exit 2 `verify ignore entry <n>: "<id>" isn't a finding ID; rhythm-roadie explain lists them`.
- An entry past its date hides nothing and adds `info.ignore-expired`, title `An ignore entry for <id> at <location> expired on <YYYY-MM-DD>, so it hides nothing now`, detail `reason: <reason>`, fix `Remove the entry, or move its until date if the value may stay.`
- Given back as `--baseline`, the report of a run hides every finding of the same copy, including a key in a theme file that moved to another line of that file; a finding in a table, column or file the baseline doesn't name is still reported.
- `VerifyResult.Hidden` counts the hidden hits (the sum of their counts). A hidden key is left out of `keys[]`; a hidden uncovered column is left out of `uncovered[]`.

## --site, --insecure-tls, --echo

- `--site <url>` (a bare host means https) is refused on a production host before any request: exit 3 `target-is-production: --site <url> is <host>, which production: lists. Roadie never fetches production. Point --site at the staging copy.` A bad URL is exit 2 `verify --site: "<s>" isn't a URL; give the staging site's address, e.g. https://staging.shop.test`.
- It fetches `/` and `/robots.txt` through kit httpx and its guard: 10 s, 2 MiB per body, user agent `rhythm-roadie verify`, redirects only within the host. Failures are listed under coverage skipped as `--site /` or `--site /robots.txt`.
- `--insecure-tls <host>` skips certificate checks only for the `--site` host.
- `--echo[=<url>]` (default `http://127.0.0.1:8025`) must be https, localhost or a loopback, private or link-local address, else exit 2 `verify --echo: <url> is plain http beyond this machine and its network, where anyone on the way could read or fake its answers. Use https:// for an Echo elsewhere.`
- `--echo` counts the messages Echo let through since `Options.EchoSince` (when the copy started); the finding is `Echo let <N> messages through since the copy`. When `EchoSince` is zero it counts from verify's own start, the finding says `since verify started` and the passing `side-effect.mail` check has the detail `counted only messages since verify started; the copy's start time wasn't known`.
- `verify.Soundcheck` runs `rhythm-soundcheck run <url> --json [--echo=<url>]` for at most 10 minutes; missing binary: note `Soundcheck not run: install it with rhythm install soundcheck`.

## Live keys

Roadie's table runs first; kit runs only when no Roadie rule matched. A test key is never a finding; an unknown one is `key.unknown-env`.

| Provider | Live | Test | Live severity |
|---|---|---|---|
| `stripe` | `sk_live_`, `rk_live_` | `sk_test_`, `rk_test_` | critical |
| `stripe-publishable` | `pk_live_` | `pk_test_` | high |
| `stripe-webhook` | `whsec_` beside `testmode` no or a `sk_live_` sibling | beside test mode or `sk_test_` (alone: unknown) | high |
| `braintree` | `access_token$production$`; unmarked key fields of the Braintree option | `access_token$sandbox$`; `sandbox_*` fields | critical |
| `square` | `EAAA`, `sq0idp-`, `sq0atp-`, `sq0csp-` with production mode | `sandbox-sq0...` or sandbox mode | critical |
| `razorpay` | `rzp_live_` | `rzp_test_` | critical |
| `mollie` | `live_` and 30 characters | `test_` and 30 | critical |
| `flutterwave` | `FLWSECK-` | `FLWSECK_TEST-` | critical |
| `adyen` | `AQE` keys where the setting says adyen, mode from `environment`/`testmode` | test mode | critical |
| `woocommerce-rest` | `ck_`/`cs_` and 40 hex | none | high |
| `paypal` | `*_production` fields, `sandbox_on` 0 | `*_sandbox` fields, `sandbox_on` 1 | critical |
| `klarna`, `authorize-net` | unmarked secret fields of their option | `test_*` fields | critical |
| `twilio` | twilio names with token, secret, sid or auth | names holding `TEST` | high |
| `jetpack` | `blog_token`, `user_tokens` | none | high |
| `aws` | `AKIA`/`ASIA` keys, secret access keys, S3 offload secrets | none | high |
| `smtp` | SMTP passwords with a host that isn't loopback, `mailpit`, `mailhog` or `echo` | those hosts | high |
| `sendgrid`, `mailgun`, `brevo`, `mailersend`, `resend`, `mailchimp`, `postmark`, `sparkpost`, `mandrill`, `ses` | their key formats or key, token or secret names | none known | high |

Placeholders never hit: empty, `xxx...`, `***`, `•`, `${VAR}`, `{{ var }}`, `<...>`, `%VAR%`, values holding `changeme`, `placeholder`, `your_`, `your-`, `example`, `redacted` or `xxxx`; also the words `null nil none true false empty test secret password undefined 0 1`. Inside any value, a key whose characters after its marker have fewer than four different characters (`xxxx`, `0000`, `XXXX`) or hold `xxxx your changeme placeholder example redacted dummy` (all lower or all upper case) is a stand-in and no hit.

### Gateway catalog

| Gateway | Option | Enabled | Mode (test / live) | Live secrets | Test secrets |
|---|---|---|---|---|---|
| `stripe` | `woocommerce_stripe_settings` | `enabled` | `testmode` yes / no | `secret_key publishable_key webhook_secret` | `test_secret_key test_publishable_key test_webhook_secret` |
| `paypal` | `woocommerce-ppcp-settings` | `enabled` | `sandbox_on` 1 / 0 | `client_id_production client_secret_production` | `client_id_sandbox client_secret_sandbox` |
| `paypal-gateway` | `woocommerce_ppcp-gateway_settings` | `enabled` | none | | |
| `woopayments` | `woocommerce_woocommerce_payments_settings` | `enabled` | `test_mode` yes / no | | |
| `square` | `wc_square_settings` | | `enable_sandbox` yes / no | `production_token production_application_id` | `sandbox_token sandbox_application_id` |
| `braintree` | `woocommerce_braintree_credit_card_settings` | `enabled` | `environment` sandbox / production | `public_key private_key` | `sandbox_public_key sandbox_private_key` |
| `mollie` | `mollie-payments-for-woocommerce_test_mode_enabled` (the option is the mode) | | yes / no | option `mollie-payments-for-woocommerce_live_api_key` | option `mollie-payments-for-woocommerce_test_api_key` |
| `razorpay` | `woocommerce_razorpay_settings` | `enabled` | `key_id` prefix `rzp_test_` / `rzp_live_` | `key_id key_secret` | |
| `klarna` | `woocommerce_klarna_payments_settings` | `enabled` | `testmode` yes / no | `merchant_id_eu shared_secret_eu merchant_id_us shared_secret_us` | `test_` of each |
| `authorize-net` | `woocommerce_authorize_net_cim_credit_card_settings` | `enabled` | `environment` test / production | `api_login_id api_transaction_key api_signature_key` | `test_` of each |

Square keeps its on switch in another option (`woocommerce_square_credit_card_settings`) and the scan reads one option at a time, so Square has no enabled guard: sandbox off flags `key.live-mode` even when the Square gateway is off.

### Mailer catalog

- `wp-mail-smtp`: option `wp_mail_smtp`, mailer `mail.mailer`; secrets `smtp.pass sendgrid.api_key mailgun.api_key sendinblue.api_key postmark.api_token sparkpost.api_key amazonses.client_secret mailjet.api_key mailjet.secret_key smtpcom.api_key elasticemail.api_key smtp2go.api_key sendlayer.api_key resend.api_key gmail.client_secret outlook.client_secret zoho.client_secret`.
- `post-smtp`: option `postman_options`, mailer `transport_type`; secrets `basic_auth_password mandrill_api_key sendgrid_api_key mailgun_api_key sendinblue_api_key postmark_api_key sparkpost_api_key elasticemail_api_key mailjet_api_key mailjet_secret_key oauth_client_secret`.
- `fluent-smtp`: option `fluentmail-settings`, mailer `misc.default_connection`; secrets `connections.*.provider_settings.password`, `.api_key`, `.secret_key`, `.client_secret`, `.access_token`.

`keys.mode` is `swap` (default) or `blank`; anything else is exit 2 `.roadie/config.yaml: keys.mode is "<mode>"; use swap or blank`. A `keys.test_keys` value that reads live is exit 2 `test-key-is-live: keys.test_keys.<name> reads a live key (<masked>). Give a test key (<hint>) or remove the entry to blank it.`

## False alarms on clean copies

`verify/testdata/clean/` holds four clean cases mirroring testbed g01 to g04: a small business shop with its own `info@` on the allow list, plugin vendor emails in update caches, wordpress.org and github.com links, totals, SKUs, EAN codes, timestamps, UUIDs, tracking numbers, RFC 5737 IPs and Stripe test keys; WooCommerce sample data with users at `example.com` and `.test`; CJK and RTL fake names, Arabic-Indic digits, fiction phones in UK, US and DE formats and IBAN-shaped order references; a clean Laravel app with test keys, `MAIL_MAILER=log` and `APP_ENV=staging`. `TestGoodInputsAreClean` asserts no finding at or above low, no failed check and no key on all four. Rows that hold Roadie fakes join the cases once the values lane's faker lands.

## Measured speed

`BenchmarkVerify` scans 210 MB of generated WooCommerce tables (posts, postmeta, options, comments, order addresses) through the real detectors, key rules and URL matcher with 8 workers. On a 16-thread i9-9900K at load average 33 to 37 (other work running): 172 to 176 MB/s, about 7.6 CPU seconds per pass. The target is 100 MB/s. Serialized values go through a stand-in for codec/nested until that lands; re-measure at integration with `go test -run '^$' -bench BenchmarkVerify ./verify/`. `ROADIE_BENCH_MB` changes the size. `BenchmarkVerifyDump` writes the same data as a MySQL dump file (one extended INSERT per 512 rows) and runs verify on it through `dump.Open`, so the dump reader's cost is in the number; it skips until the io lane's reader lands. A run at load average 46 (other lanes building) gave 39 MB/s for `BenchmarkVerify`, which is load, not a measure; re-measure both idle at integration.
