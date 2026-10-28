# Engine lane: user-visible changes

What the `engine` lane (config, profiles and the pipeline: `roadiecfg/`, `profile/`, `engine/`) changes for users, for the docs team. Exact strings are in backticks. The lane's working notes with every batch are in the review folder; this file is the summary to build the wiki pages from.

## 1. `.roadie/config.yaml`

### Where it is

- Roadie walks up from the working folder to the nearest `.roadie/config.yaml`, as git finds `.git`. It stops at the repository root (the folder holding `.git` is still searched) and below `$HOME` (a stray `~/.roadie` never catches every project). Nothing found: `no .roadie/config.yaml here or above; run rhythm-roadie config init`.
- The project folder is the parent of `.roadie/`. `production:` lists from `.echo/config.yaml` and `.soundcheck/config.yaml` in that folder are merged into Roadie's (only `production:` is read there). A bad entry in those files stops the run with exit 2, since a missed production site could become a target.

### Keys and defaults

Every key is optional. The defaults (`roadiecfg/testdata/golden/default.yaml`):

| Key | Default | Values |
|---|---|---|
| `source`, `target`, `url` | empty | an endpoint (PRD 6.2); `url` an http(s) URL |
| `production` | `[]` | `shop.com`, `*.shop.com` or `https://shop.com/shop/` |
| `url_map` | `{}` | `from: to` host or URL pairs; an empty value excludes a host from rewriting |
| `stack` | `auto` | auto, wordpress, laravel, sql |
| `profiles` | `[auto]` | `auto` or profile IDs |
| `remote.wp` | `wp` | the path of WP-CLI on the remote host |
| `pseudonymise.email_domain` | `example.invalid` | a reserved domain only |
| `pseudonymise.phone_style` | `echo` | echo, country |
| `pseudonymise.locale` | `en` | en, de, fr, es, nl, it |
| `pseudonymise.keep` | `[country, state]` | country, state, city, postcode |
| `pseudonymise.keep_users` | `[]` | login or email globs |
| `pseudonymise.consistency` | `per-run` | per-run, stable |
| `pseudonymise.on_unparseable` | `blank` | blank, fail |
| `pseudonymise.passwords` | `reset` | reset, keep |
| `pseudonymise.free_text` | `scrub` | scrub, blank, keep |
| `drop`, `truncate` | `[]` | table globs |
| `columns` | `[]` | `{table, column, where, rule}` entries |
| `keys.mode` | `swap` | swap, blank |
| `keys.test_keys` | `{}` | provider to test key, e.g. `stripe: ${ROADIE_STRIPE_TEST}` |
| `keys.ignore`, `verify.ignore` | `[]` | `{id, location, reason, until}` |
| `side_effects.echo.install` | `true` | |
| `side_effects.echo.url` | `http://127.0.0.1:8025` | |
| `side_effects.echo.mode` | `seal` | seal, observe |
| `side_effects.echo.proxy` | empty | |
| `side_effects.cron` | `pause` | pause, keep |
| `side_effects.action_scheduler` | `pause` | pause, cancel, keep |
| `side_effects.webhooks` | `pause` | pause, keep |
| `side_effects.gateways` | `test-mode` | test-mode, keep |
| `side_effects.subscriptions` | `staging` | staging, keep |
| `side_effects.mail` | `seal` | seal, keep |
| `noindex` | `true` | |
| `basic_auth` | `false` | `true` writes the snippets for user `staging` |
| `verify.fail_on` | `high` | none, info, low, medium, high, critical |
| `verify.allow_emails`, `verify.allow_phones`, `verify.skip` | `[]` | |
| `verify.sign` | `true` | |
| `laravel.env_file` | `.env` | a relative path without `..` |
| `laravel.tables`, `laravel.env` | `{}` | table to column to rule; .env names to values |

Rules a `columns:` entry or `laravel.tables` may name: email, phone, name (full_name), first_name, last_name, full_name, address, street, city, postcode, ip, dob, company, username, nicename, url, user_agent, text (scrub), blank, null, keep, `fixed:<value>`, `json:<path>=<rule>`, `serialized:<key>=<rule>`, plus password, delete-row, rewrite-url and swap-key.

### Validation messages (all exit 2, code `config`)

The file is called `.roadie/config.yaml` in messages when it is in the usual place. The line is the key's line.

- `.roadie/config.yaml line 3: unknown key pseudonymise.emial_domain; did you mean pseudonymise.email_domain?` (no close match: `...; every key is listed at https://github.com/RhythmShahriar/rhythm/wiki/Roadie-Configuration`)
- `.roadie/config.yaml line 1: stack is a list; it takes a text value` (kinds: `a text value`, `true or false`, `a list, e.g. [a, b]`, `a map of names to values, e.g. {a: b}`, `a group of settings`; a quoted `"yes"` is not a boolean)
- `.roadie/config.yaml line 2: stack is set twice (first on line 1); keep one`
- `<file> line N: <key> uses an alias (*x); write the value out instead` (YAML aliases are refused, so alias bombs can't load)
- `<file> line N: this isn't valid YAML: <yaml's words>` and `<file> line 1: the file must be a list of settings like "source:" and "stack: auto"`
- `.roadie/config.yaml line 1: drop[1] is "wp_[", which isn't a valid glob (use *, ? and [abc] as in path.Match)`
- `.roadie/config.yaml line 4: keys.test_keys.stripe reads ${STRIPE_TEST}; only ROADIE_ variables are read. Rename it ROADIE_STRIPE_TEST.`
- `.roadie/config.yaml line 5: keys.test_keys.stripe reads ${ROADIE_STRIPE_TEST_SECRET}, which isn't set. Set it in the environment, or remove the entry.` (`plan`, `copy`, `prepare` and `verify` stop with it, exit 2, before the lock when a key they use reads an unset variable; a key a flag replaces, such as `url:` under `--url`, doesn't stop them; `config check` lists every unset one, `production:` and `verify.allow_emails` included)
- `.roadie/config.yaml line 2: pseudonymise.email_domain is "shop.com", which can receive real mail. Use a reserved domain: example.invalid, example.com or a name under .test, .example, .invalid or .localhost.`
- `stack is "wordpres"; did you mean wordpress? It takes auto, wordpress, laravel or sql.` and the same shape for every other enum.
- `verify.ignore[0] needs a reason: say why this value may stay`, `keys.ignore[0].id is "pii.phnoe", which isn't a finding ID; did you mean pii.phone? rhythm-roadie explain lists them.`, `keys.ignore[0].until is "soon"; write a date like 2027-01-31`
- `columns[0].rule: "emial" isn't a rule; did you mean email? Rules: <list>`
- `production[1] is "ftp://shop.com", which isn't a site like shop.com, *.shop.com or https://shop.com/shop/`
- `keys.test_keys.strpe names "strpe", which isn't a provider Roadie swaps keys for; did you mean stripe?`
- A live key given as a test key: the keys lane's `test-key-is-live` message with `.roadie/config.yaml line N: ` in front.
- `.roadie/config.yaml line 2: side_effects.echo.url is http://echo.example.com:8025 over plain http to a public host; use https or an address on this machine or a private network` (plain http is fine to loopback, private and link-local IPs, `localhost`, single-label names and names under `.local`, `.localhost`, `.internal`, `.lan`, `.home.arpa`)

`${ROADIE_NAME}` is read only in string values. `config show` masks test keys and every interpolated value (`sk_t•••••`); DSN and proxy passwords show as `•••`, in the user info and in a `password`, `pass`, `pwd` or `sslpassword` query parameter (any case): `postgres://u@db.example/shop?sslmode=require&password=•••`. The user-info password is found on the raw text (first `:` after `://` to the last `@` before the query), so an unencoded `#` or `/` in it is masked too: `mysql://root:•••@db/shop`. A query password runs to the next `&`, `#` included. A `source:` or `target:` the parser refuses is named masked; when it carries a password the parser's own text is left out: `source: "mysql://root:•••@db/shop" isn't a site path, ssh:// address, @alias, dump file or DSN Roadie can read` (exit 2, code `config`). Each value names its source: flag, file, echo, soundcheck or default.

A validation error about a value read from the environment shows the `${ROADIE_NAME}` reference where the value would stand, never the value: `.roadie/config.yaml line 1: url is "${ROADIE_URL}", which isn't a URL like https://staging.shop.test`. A value of three bytes or less is replaced only where it stands quoted.

### `config init`

- WordPress and WooCommerce (and `auto`) get the PRD 8.1 file byte for byte (`roadiecfg/testdata/golden/init-wordpress.yaml`). Laravel (`init-laravel.yaml`) and SQL (`init-sql.yaml`) get their own: Laravel marks the WordPress-only side effects and `noindex` as such and shows `laravel.tables`; SQL starts with an active `columns:` example.
- Messages: `Wrote .roadie/config.yaml for a WooCommerce site.`, `... for a WordPress site.`, `... for a Laravel app.`, `... for a generic SQL database.`
- Git: `.roadie/reports/`, `.roadie/key`, `.roadie/backups/` and `.roadie/basic-auth/` go into `.git/info/exclude` (of the main repository for a linked worktree) under `# rhythm-roadie: reports, keys and backups stay out of git`, anchored at the work tree root. Glob characters in the project's path (`*`, `?`, `[`, `]`, `\`) are backslash-escaped, so a folder named `shop[1]` is excluded too.
- `config init --from <source>` reads the schema with `--stack` when given (`woocommerce` reads as `wordpress`); a dump no profile matches (a plain SQL or PostgreSQL dump, a Laravel dump without `migrations`) is read as stack `sql` and gets the `sql` template.
- `config init --from <dump>` adds the uncovered columns as commented suggestions before `keys:` (`roadiecfg/testdata/golden/init-from.yaml`): `#    rule: <guess>  # 98% of sampled values look personal` or `# the name looks personal`. A table or column name that isn't plain (letters, digits, `_`, `$`, `.`, `-`) is written as a double-quoted YAML string, so a name holding a newline can't become an active setting: `#    column: "notes\nsource: ..."`.

## 2. The consistency key

- `ROADIE_KEY` (base64 of 32 bytes) wins, then `.roadie/key`, else a new random key.
- `consistency: per-run` (default) without `--keep-key`: a new key each run, never saved; the report's key fingerprint is null.
- `consistency: stable` or `--keep-key`: the saved key; a new one is saved to `.roadie/key` (0600, folder 0700). The report shows `roadie-key:` and 12 base32 characters, never the key.
- Errors: `ROADIE_KEY isn't base64 of 32 bytes. Make one with rhythm-roadie config key rotate, or unset it.` and `.roadie/key isn't base64 of 32 bytes. Run rhythm-roadie config key rotate to make a new one (fakes change once).`

## 3. Profiles

A profile names the tables and columns of one stack or plugin and the rule for each. `auto` picks the detected profiles of the stack plus the stack's base profile. `{prefix}` is the WordPress table prefix (from `$table_prefix`, else the options table); `{site}` is the main site and every multisite blog (`2_`, `11_`), matched exactly so `wp_1_` never takes `wp_11_` tables.

- A `columns:` rule replaces the profile's rule on the same table, column, where clause and path; without a where clause it replaces every profile rule on that column. A `columns:` table glob (`wp_*meta`) replaces the profile rules on every table it matches the same way.
- Table actions, first match wins: config drop, config truncate, a table `columns:` names, profile drop, profile truncate, profile keep, has rules, else kept.
- `pseudonymise.keep` keeps the address parts it names; `free_text: blank|keep` turns text rules into blank or keep; `passwords: keep` keeps password hashes.
- Errors (exit 2): `profiles names "mini-cor", which isn't a profile; did you mean mini-core? rhythm-roadie profiles lists them.`, `profiles names laravel, which is for laravel; this is a wordpress schema. Remove it or set stack:.`, `stack: no profile matches this dump (no wp_options or migrations table). For a Laravel app with no migrations table, set stack: laravel (or pass --stack laravel); for your own schema, list the tables under columns: and set stack: sql.`
- Field-type row fixes read the form definitions in pass 1 and add a rule per field: Gravity Forms (`gf_form_meta.display_meta`), WPForms (`wpforms` posts) and Ninja Forms (`nf3_fields`). Email fields become email, phone phone, name parts first and last names, address parts street, city, state, postcode and country.
- Detectors: `tables_any` (a table), `columns_any` (a `table.column` pair, for a profile that adds columns to a core table), `options_any` (an option name) and `files` (a file on the site). The stack and the file detectors ask the source's site; with a dump source they ask the target's site (not a database's), so a Laravel dump with no migrations table copied into a Laravel site is read as Laravel.
- Fixtures: every rule has a positive and a negative row in `profile/testdata/fixtures/`.

The built-in profiles, as `rhythm-roadie profiles` lists them (rule `text` scrubs names, emails, phones and IPs out of free text; `reset-password` gives an unusable hash):

#### `action-scheduler` (Action Scheduler)

- Stack: wordpress. Checked against: `docs: https://raw.githubusercontent.com/woocommerce/action-scheduler/3.9.2/classes/schema/ActionScheduler_StoreSchema.php`.
- Detected by: table `{prefix}{site}actionscheduler_actions`.
- Row fixes: action-scheduler-args.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}actionscheduler_actions` | `args` |  |  | text | the action's arguments as JSON (with serialized values inside); the action-scheduler-args row fix rehashes them |
| `{prefix}{site}actionscheduler_actions` | `extended_args` |  |  | text | arguments over 191 bytes; args then holds their md5 |
| `{prefix}{site}actionscheduler_logs` | `message` |  |  | text | log lines that can name customers |

#### `automatewoo` (AutomateWoo)

- Stack: wordpress. Checked against: `docs: https://woocommerce.com/document/automatewoo/regenerate-automatewoo-database-tables/`.
- Detected by: table `{prefix}{site}automatewoo_queue`, table `{prefix}{site}automatewoo_guests`, file `wp-content/plugins/automatewoo/automatewoo.php`.
- Truncated: `{prefix}{site}automatewoo_abandoned_carts`, `{prefix}{site}automatewoo_customers`, `{prefix}{site}automatewoo_queue`, `{prefix}{site}automatewoo_queue_meta`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}automatewoo_guests` | `email` |  |  | email | a guest's email captured at checkout |
| `{prefix}{site}automatewoo_guest_meta` | `meta_value` |  |  | text | guest details such as names and phones |
| `{prefix}{site}automatewoo_log_meta` | `meta_value` |  |  | text | workflow log details that can name customers |

#### `contact-form-7-flamingo` (Contact Form 7 with Flamingo)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/flamingo/trunk/includes/class-inbound-message.php`.
- Detected by: option `wpcf7`, file `wp-content/plugins/flamingo/flamingo.php`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}posts` | `post_content` | `post_type=flamingo_inbound` |  | text | the message with every field the sender filled in |
| `{prefix}{site}posts` | `post_title` | `post_type=flamingo_inbound` |  | text | the message subject |
| `{prefix}{site}posts` | `post_title` | `post_type=flamingo_contact` |  | email | a contact's title is the sender's email |
| `{prefix}{site}posts` | `post_name` | `post_type=flamingo_contact` |  | nicename | the slug, made from the sender's email |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_from` |  | text | the sender as name and email |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_from_email` |  | email | the sender's email |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_from_name` |  | full_name | the sender's name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_field_your-email` |  | email | the email field of Contact Form 7's default form (your-email) |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_field_your-name` |  | full_name | the name field of Contact Form 7's default form (your-name) |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_field_*` |  | text | one form field of the message |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_meta` | `serialized:remote_ip` | ip | the sender's IP address in the request details |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_meta` | `serialized:user_agent` | user_agent | the sender's browser in the request details |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_email` |  | email | a contact's email |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_name` |  | full_name | a contact's name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_props` |  | text | a contact's name parts, serialized |

#### `edd` (Easy Digital Downloads)

- Stack: wordpress. Checked against: `docs: https://github.com/awesomemotive/easy-digital-downloads/tree/main/includes/database/schemas`.
- Detected by: table `{prefix}{site}edd_customers`, table `{prefix}{site}edd_orders`, file `wp-content/plugins/easy-digital-downloads/easy-digital-downloads.php`.
- Truncated: `{prefix}{site}edd_logs_api_requests`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}edd_customers` | `email` |  |  | email | the customer's email |
| `{prefix}{site}edd_customers` | `name` |  |  | full_name | the customer's name |
| `{prefix}{site}edd_customermeta` | `meta_value` | `meta_key=additional_email` |  | email | a further email of the customer |
| `{prefix}{site}edd_customer_email_addresses` | `email` |  |  | email | every email the customer used |
| `{prefix}{site}edd_customer_addresses` | `name` |  |  | full_name | the address's name |
| `{prefix}{site}edd_customer_addresses` | `address` |  |  | street | the street |
| `{prefix}{site}edd_customer_addresses` | `address2` |  |  | street | the second address line |
| `{prefix}{site}edd_customer_addresses` | `city` |  |  | city | the city |
| `{prefix}{site}edd_customer_addresses` | `region` |  |  | blank | the region, kept by default for tax |
| `{prefix}{site}edd_customer_addresses` | `postal_code` |  |  | postcode | the postcode |
| `{prefix}{site}edd_customer_addresses` | `country` |  |  | blank | the country, kept by default for tax |
| `{prefix}{site}edd_orders` | `email` |  |  | email | the order's email |
| `{prefix}{site}edd_orders` | `ip` |  |  | ip | the IP address the order came from |
| `{prefix}{site}edd_order_addresses` | `name` |  |  | full_name | the order address's name |
| `{prefix}{site}edd_order_addresses` | `address` |  |  | street | the order's street |
| `{prefix}{site}edd_order_addresses` | `address2` |  |  | street | the order's second address line |
| `{prefix}{site}edd_order_addresses` | `city` |  |  | city | the order's city |
| `{prefix}{site}edd_order_addresses` | `region` |  |  | blank | the order's region |
| `{prefix}{site}edd_order_addresses` | `postal_code` |  |  | postcode | the order's postcode |
| `{prefix}{site}edd_order_addresses` | `country` |  |  | blank | the order's country |
| `{prefix}{site}edd_order_transactions` | `transaction_id` |  |  | blank | the gateway's transaction ID |
| `{prefix}{site}edd_logs` | `title` |  |  | text | log titles that can name customers |
| `{prefix}{site}edd_logs` | `content` |  |  | text | log text that can quote customers |
| `{prefix}{site}edd_logs_file_downloads` | `ip` |  |  | ip | the IP address of a file download |
| `{prefix}{site}edd_logs_file_downloads` | `user_agent` |  |  | user_agent | the browser of a file download |
| `{prefix}{site}edd_notes` | `content` |  |  | text | notes on orders and customers |
| `{prefix}{site}edd_logs_emails` | `email` |  |  | email | the recipient of a mail EDD sent (the email log of EDD 3.3) |
| `{prefix}{site}edd_logs_emails` | `subject` |  |  | text | the subject of a mail EDD sent, which can name the customer |

#### `email-log` (Email Log)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/email-log/trunk/include/Core/DB/TableManager.php`.
- Detected by: table `{prefix}{site}email_log`, file `wp-content/plugins/email-log/email-log.php`.
- Truncated: `{prefix}{site}email_log`.

#### `fluent-forms` (Fluent Forms)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/fluentform/trunk/database/Migrations/Submissions.php`.
- Detected by: table `{prefix}{site}fluentform_forms`, table `{prefix}{site}fluentform_submissions`, file `wp-content/plugins/fluentform/fluentform.php`.
- Truncated: `{prefix}{site}fluentform_draft_submissions`, `{prefix}{site}fluentform_form_analytics`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}fluentform_submissions` | `response` |  |  | text | every field of the submission as JSON |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:email` | email | the email field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:names.first_name` | first_name | the first name of the name field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:names.middle_name` | first_name | the middle name of the name field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:names.last_name` | last_name | the last name of the name field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:phone` | phone | the phone field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.address_line_1` | street | the street of the address field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.address_line_2` | street | the second line of the address field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.city` | city | the city of the address field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.zip` | postcode | the postcode of the address field as a new form names it |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.state` | blank | the state of the address field as a new form names it; kept by default |
| `{prefix}{site}fluentform_submissions` | `response` |  | `json:address_1.country` | blank | the country of the address field as a new form names it; kept by default |
| `{prefix}{site}fluentform_submissions` | `source_url` |  |  | rewrite-url | the page the form was on |
| `{prefix}{site}fluentform_submissions` | `ip` |  |  | ip | the IP address the submission came from |
| `{prefix}{site}fluentform_submissions` | `city` |  |  | city | the city looked up from the IP address |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=email` |  | email | the email field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=phone` |  | phone | the phone field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=names, sub_field_name=first_name` |  | first_name | the first name of the name field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=names, sub_field_name=middle_name` |  | first_name | the middle name of the name field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=names, sub_field_name=last_name` |  | last_name | the last name of the name field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=address_line_1` |  | street | the street of the address field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=address_line_2` |  | street | the second line of the address field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=city` |  | city | the city of the address field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=zip` |  | postcode | the postcode of the address field as a new form names it |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=state` |  | blank | the state of the address field as a new form names it; kept by default |
| `{prefix}{site}fluentform_entry_details` | `field_value` | `field_name=address_1, sub_field_name=country` |  | blank | the country of the address field as a new form names it; kept by default |
| `{prefix}{site}fluentform_entry_details` | `field_value` |  |  | text | one field's value |
| `{prefix}{site}fluentform_submission_meta` | `value` |  |  | text | notes and data attached to a submission |
| `{prefix}{site}fluentform_logs` | `description` |  |  | text | log lines that quote submissions |

#### `fluent-smtp` (FluentSMTP)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/fluent-smtp/trunk/database/migrations/EmailLogs.php`.
- Detected by: table `{prefix}{site}fsmpt_email_logs`, file `wp-content/plugins/fluent-smtp/fluent-smtp.php`.
- Truncated: `{prefix}{site}fsmpt_email_logs`.

#### `gravity-forms` (Gravity Forms)

- Stack: wordpress. Checked against: `docs: https://docs.gravityforms.com/database-storage-structure-reference/`.
- Detected by: table `{prefix}{site}gf_form`, table `{prefix}{site}gf_entry`, file `wp-content/plugins/gravityforms/gravityforms.php`.
- Row fixes: gf-field-types.
- Truncated: `{prefix}{site}gf_draft_submissions`, `{prefix}{site}gf_form_view`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}gf_entry` | `ip` |  |  | ip | the IP address the entry came from |
| `{prefix}{site}gf_entry` | `user_agent` |  |  | user_agent | the browser the entry came from |
| `{prefix}{site}gf_entry` | `source_url` |  |  | rewrite-url | the page the form was on, rewritten to the staging URL |
| `{prefix}{site}gf_entry` | `transaction_id` |  |  | blank | the gateway's transaction ID of a payment form |
| `{prefix}{site}gf_entry_meta` | `meta_value` |  |  | text | field values; gf-field-types gives typed fields their own rule and the rest are scrubbed as free text |
| `{prefix}{site}gf_entry_notes` | `value` |  |  | text | notes on an entry, often quoting the person |
| `{prefix}{site}gf_entry_notes` | `user_name` |  |  | full_name | the name of the staff member who wrote the note |

#### `laravel` (Laravel)

- Stack: laravel. Checked against: `docs: https://github.com/laravel/laravel/blob/12.x/database/migrations/0001_01_01_000000_create_users_table.php`.
- Detected by: table `migrations`.
- Truncated: `cache`, `cache_locks`, `failed_jobs`, `job_batches`, `jobs`, `password_reset_tokens`, `password_resets`, `personal_access_tokens`, `pulse_*`, `sessions`, `telescope_entries`, `telescope_entries_tags`, `telescope_monitoring`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `users` | `name` |  |  | full_name | the account's name |
| `users` | `email` |  |  | email | the account's email |
| `users` | `password` |  |  | reset-password | the password hash; reset so no real password works on the copy |
| `users` | `remember_token` |  |  | null | the remember-me token that would log in to the copy |
| `users` | `two_factor_secret` |  |  | null | the Fortify two-factor secret, encrypted with the old APP_KEY |
| `users` | `two_factor_recovery_codes` |  |  | null | the Fortify recovery codes |
| `notifications` | `data` |  |  | text | the notification's JSON, which often names the person |

#### `laravel-cashier` (Laravel Cashier)

- Stack: laravel. Checked against: `docs: https://github.com/laravel/cashier-stripe/tree/15.x/database/migrations`.
- Detected by: table `subscription_items`, column `users.stripe_id` (a shop with one-off charges has Cashier's customer column but no subscription tables).
- Kept as they are: `subscription_items`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `users` | `stripe_id` |  |  | null | the live Stripe customer ID; null so the copy can't charge it |
| `users` | `pm_type` |  |  | null | the saved card's brand |
| `users` | `pm_last_four` |  |  | null | the saved card's last four digits |
| `users` | `trial_ends_at` |  |  | keep | a date, kept so trials behave the same |
| `subscriptions` | `stripe_id` |  |  | keep | the subscription's Stripe ID, useless without the live key |
| `subscriptions` | `stripe_price` |  |  | keep | the price ID, needed to show the plan |

#### `learndash` (LearnDash)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/wp-pro-quiz/trunk/lib/helper/WpProQuiz_Helper_DbUpgrade.php`.
- Detected by: table `{prefix}{site}learndash_user_activity`, table `{prefix}{site}learndash_pro_quiz_toplist`, file `wp-content/plugins/sfwd-lms/sfwd_lms.php`.
- Kept as they are: `{prefix}{site}learndash_user_activity`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}learndash_pro_quiz_toplist` | `name` |  |  | full_name | the name on the leaderboard |
| `{prefix}{site}learndash_pro_quiz_toplist` | `email` |  |  | email | the email on the leaderboard |
| `{prefix}{site}learndash_pro_quiz_toplist` | `ip` |  |  | ip | the IP address of the attempt |
| `{prefix}{site}learndash_pro_quiz_statistic_ref` | `form_data` |  |  | text | the quiz's custom form fields, such as name and email |

#### `mailpoet` (MailPoet)

- Stack: wordpress. Checked against: `docs: https://github.com/mailpoet/mailpoet/blob/trunk/mailpoet/lib/Entities/SubscriberEntity.php`.
- Detected by: table `{prefix}{site}mailpoet_subscribers`, file `wp-content/plugins/mailpoet/mailpoet.php`.
- Truncated: `{prefix}{site}mailpoet_log`, `{prefix}{site}mailpoet_sending_queues`.
- Kept as they are: `{prefix}{site}mailpoet_statistics_*`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}mailpoet_subscribers` | `email` |  |  | email | the subscriber's email |
| `{prefix}{site}mailpoet_subscribers` | `first_name` |  |  | first_name | the subscriber's first name |
| `{prefix}{site}mailpoet_subscribers` | `last_name` |  |  | last_name | the subscriber's last name |
| `{prefix}{site}mailpoet_subscribers` | `subscribed_ip` |  |  | ip | the IP address of the sign-up |
| `{prefix}{site}mailpoet_subscribers` | `confirmed_ip` |  |  | ip | the IP address of the confirmation click |
| `{prefix}{site}mailpoet_subscribers` | `unconfirmed_data` |  |  | text | a change waiting for confirmation, as JSON |
| `{prefix}{site}mailpoet_subscriber_custom_field` | `value` |  |  | text | custom sign-up fields such as a phone or a birthday |
| `{prefix}{site}mailpoet_subscriber_ips` | `ip` |  |  | ip | sign-up IP addresses kept for rate limits |

#### `memberpress` (MemberPress)

- Stack: wordpress. Checked against: `docs: https://memberpress.com/docs/where-is-data-stored/`.
- Detected by: table `{prefix}{site}mepr_transactions`, table `{prefix}{site}mepr_members`, file `wp-content/plugins/memberpress/memberpress.php`.
- Kept as they are: `{prefix}{site}mepr_members`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}mepr_transactions` | `ip_addr` |  |  | ip | the IP address of the payment |
| `{prefix}{site}mepr_transactions` | `trans_num` |  |  | blank | the gateway's transaction ID |
| `{prefix}{site}mepr_transactions` | `response` |  |  | blank | the gateway's raw answer, with card and customer details |
| `{prefix}{site}mepr_subscriptions` | `subscr_id` |  |  | blank | the gateway's subscription ID, which a renewal would charge |
| `{prefix}{site}mepr_subscriptions` | `ip_addr` |  |  | ip | the IP address of the sign-up |
| `{prefix}{site}mepr_subscriptions` | `token` |  |  | blank | the saved payment token |
| `{prefix}{site}mepr_subscriptions` | `cc_last4` |  |  | blank | the last four card digits |
| `{prefix}{site}mepr_subscriptions` | `response` |  |  | blank | the gateway's raw answer |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-one` |  | street | the member's street |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-two` |  | street | the member's second address line |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-city` |  | city | the member's city |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-zip` |  | postcode | the member's postcode |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-state` |  | blank | the member's state, kept by default for tax |
| `{prefix}usermeta` | `meta_value` | `meta_key=mepr-address-country` |  | blank | the member's country, kept by default for tax |

#### `multisite` (WordPress multisite)

- Stack: wordpress. Checked against: `docs: https://raw.githubusercontent.com/WordPress/WordPress/6.8-branch/wp-admin/includes/schema.php`.
- Detected by: table `{prefix}blogs`, table `{prefix}sitemeta`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}blogs` | `domain` |  |  | rewrite-url | the blog's host, a bare host rewritten to its staging host |
| `{prefix}blogs` | `path` |  |  | rewrite-url | the blog's path, rewritten only when the staging URL has another path |
| `{prefix}site` | `domain` |  |  | rewrite-url | the network's host |
| `{prefix}site` | `path` |  |  | rewrite-url | the network's path |
| `{prefix}sitemeta` | `meta_value` | `meta_key=admin_email` |  | email | the network admin's email |
| `{prefix}sitemeta` | `meta_value` | `meta_key=site_admins` | `serialized:*` | username | the logins of the super admins, a serialized list |
| `{prefix}sitemeta` | `meta_value` | `meta_key=siteurl` |  | rewrite-url | the network URL |
| `{prefix}sitemeta` | `meta_value` | `meta_key=*domain*` |  | rewrite-url | a network setting named for a domain (a domain-mapping plugin's, for example); a bare host is rewritten to its staging host |
| `{prefix}signups` | `user_email` |  |  | email | the email of a pending sign-up |
| `{prefix}signups` | `user_login` |  |  | username | the login a sign-up asked for |
| `{prefix}signups` | `meta` |  |  | text | sign-up details, a serialized array |
| `{prefix}signups` | `activation_key` |  |  | blank | the key that activates the sign-up |
| `{prefix}registration_log` | `email` |  |  | email | who registered a blog |
| `{prefix}registration_log` | `IP` |  |  | ip | the IP address of the registration |
| `{prefix}blogmeta` | `meta_value` |  |  | text | blog metadata of any plugin, scrubbed as free text |

#### `ninja-forms` (Ninja Forms)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/ninja-forms/trunk/includes/Database/Migrations/Fields.php`.
- Detected by: table `{prefix}{site}nf3_forms`, table `{prefix}{site}nf3_fields`, file `wp-content/plugins/ninja-forms/ninja-forms.php`.
- Row fixes: ninja-field-types.
- Truncated: `{prefix}{site}nf3_chunks`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_field_*` |  | text | a submission's field value; ninja-field-types fakes typed fields and the rest are scrubbed |
| `{prefix}{site}nf3_object_meta` | `value` |  |  | text | settings of saved objects, which can hold submitted values |
| `{prefix}{site}nf3_object_meta` | `meta_value` |  |  | text | the same settings in the newer column |

#### `post-smtp` (Post SMTP)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/post-smtp/trunk/Postman/Postman-Email-Log/PostmanEmailLogService.php`.
- Detected by: table `{prefix}{site}post_smtp_logs`, table `{prefix}{site}post_smtp_logmeta`, file `wp-content/plugins/post-smtp/postman-smtp.php`.
- Truncated: `{prefix}{site}post_smtp_logmeta`, `{prefix}{site}post_smtp_logs`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}posts` | `post_content` | `post_type=postman_sent_mail` |  | delete-row | a mail logged by Post SMTP before 2.5, kept as a post |

#### `sql` (Generic SQL)

- Stack: sql. Checked against: `config: the tables columns: lists`.

#### `woocommerce` (WooCommerce)

- Stack: wordpress. Checked against: `docs: https://raw.githubusercontent.com/woocommerce/woocommerce/9.8.0/plugins/woocommerce/includes/class-wc-install.php`.
- Detected by: table `{prefix}{site}woocommerce_order_items`, table `{prefix}{site}wc_customer_lookup`, option `woocommerce_version`, file `wp-content/plugins/woocommerce/woocommerce.php`.
- Row fixes: hpos-address-index.
- Truncated: `{prefix}{site}wc_admin_note_actions`, `{prefix}{site}woocommerce_api_keys`, `{prefix}{site}woocommerce_payment_tokenmeta`, `{prefix}{site}woocommerce_payment_tokens`, `{prefix}{site}woocommerce_sessions`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_email` |  | email | the order's billing email |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_phone` |  | phone | the order's billing phone |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_first_name` |  | first_name | the order's billing first name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_last_name` |  | last_name | the order's billing last name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_company` |  | company | the order's billing company |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_address_1` |  | street | the order's billing street |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_address_2` |  | street | the order's billing second address line |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_city` |  | city | the order's billing city |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_postcode` |  | postcode | the order's billing postcode |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_country` |  | blank | the order's billing country |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_state` |  | blank | the order's billing state |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_address_index` |  | text | the billing search index; the hpos-address-index row fix rebuilds it from the fake parts |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_phone` |  | phone | the order's shipping phone |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_first_name` |  | first_name | the order's shipping first name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_last_name` |  | last_name | the order's shipping last name |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_company` |  | company | the order's shipping company |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_address_1` |  | street | the order's shipping street |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_address_2` |  | street | the order's shipping second address line |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_city` |  | city | the order's shipping city |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_postcode` |  | postcode | the order's shipping postcode |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_country` |  | blank | the order's shipping country |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_state` |  | blank | the order's shipping state |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_shipping_address_index` |  | text | the shipping search index; the hpos-address-index row fix rebuilds it from the fake parts |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_customer_ip_address` |  | ip | the IP address the order came from |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_customer_user_agent` |  | user_agent | the browser the order came from |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_stripe_customer_id` |  | blank | the Stripe customer ID, which reaches the live Stripe account |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_stripe_source_id` |  | blank | the Stripe payment source ID |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_stripe_intent_id` |  | blank | the Stripe payment intent ID |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_payment_tokens` |  | blank | saved payment token IDs |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_billing_vat_number` |  | blank | the customer's VAT number |
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_wcpay_*customer*` |  | blank | WooPayments customer IDs |
| `{prefix}{site}posts` | `post_excerpt` | `post_type=shop_order*` |  | text | the customer's note on a legacy order (a refund's reason on a refund); free text |
| `{prefix}{site}wc_customer_lookup` | `first_name` |  |  | first_name | the customer's first name in the analytics lookup |
| `{prefix}{site}wc_customer_lookup` | `last_name` |  |  | last_name | the customer's last name |
| `{prefix}{site}wc_customer_lookup` | `email` |  |  | email | the customer's email |
| `{prefix}{site}wc_customer_lookup` | `username` |  |  | username | the customer's login |
| `{prefix}{site}wc_customer_lookup` | `city` |  |  | city | the customer's city |
| `{prefix}{site}wc_customer_lookup` | `postcode` |  |  | postcode | the customer's postcode |
| `{prefix}{site}wc_customer_lookup` | `country` |  |  | blank | the customer's country, kept by default like the order's |
| `{prefix}{site}wc_customer_lookup` | `state` |  |  | blank | the customer's state, kept by default like the order's |
| `{prefix}{site}woocommerce_downloadable_product_permissions` | `user_email` |  |  | email | the email a download was granted to |
| `{prefix}{site}wc_download_log` | `user_ip_address` |  |  | ip | the IP address of a download |
| `{prefix}{site}woocommerce_log` | `message` |  |  | text | WooCommerce log lines (the database log handler), which can name customers and their emails |
| `{prefix}{site}woocommerce_log` | `context` |  |  | text | the log line's context, a serialized array that can hold order and customer details |
| `{prefix}{site}wc_webhooks` | `status` |  |  | fixed:paused | webhooks paused so the copy never calls the live endpoints |
| `{prefix}{site}wc_webhooks` | `secret` |  |  | blank | the webhook signing secret |
| `{prefix}{site}wc_webhooks` | `delivery_url` |  |  | keep | kept so the webhook can be checked; it is paused |

#### `woocommerce-hpos` (WooCommerce HPOS orders)

- Stack: wordpress. Checked against: `docs: https://raw.githubusercontent.com/woocommerce/woocommerce/9.8.0/plugins/woocommerce/src/Internal/DataStores/Orders/OrdersTableDataStore.php`.
- Detected by: table `{prefix}{site}wc_orders`.
- Row fixes: hpos-address-index.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}wc_orders` | `billing_email` |  |  | email | the order's billing email |
| `{prefix}{site}wc_orders` | `ip_address` |  |  | ip | the IP address the order came from |
| `{prefix}{site}wc_orders` | `user_agent` |  |  | user_agent | the browser the order came from |
| `{prefix}{site}wc_orders` | `customer_note` |  |  | text | the note the customer left at checkout |
| `{prefix}{site}wc_orders` | `transaction_id` |  |  | blank | the gateway's transaction ID, which reaches the live account |
| `{prefix}{site}wc_order_addresses` | `first_name` |  |  | first_name | the address's first name |
| `{prefix}{site}wc_order_addresses` | `last_name` |  |  | last_name | the address's last name |
| `{prefix}{site}wc_order_addresses` | `company` |  |  | company | the address's company |
| `{prefix}{site}wc_order_addresses` | `address_1` |  |  | street | the street |
| `{prefix}{site}wc_order_addresses` | `address_2` |  |  | street | the second address line |
| `{prefix}{site}wc_order_addresses` | `city` |  |  | city | the city |
| `{prefix}{site}wc_order_addresses` | `postcode` |  |  | postcode | the postcode |
| `{prefix}{site}wc_order_addresses` | `country` |  |  | blank | the country, kept by default for tax and shipping |
| `{prefix}{site}wc_order_addresses` | `state` |  |  | blank | the state, kept by default for tax and shipping |
| `{prefix}{site}wc_order_addresses` | `email` |  |  | email | the address's email |
| `{prefix}{site}wc_order_addresses` | `phone` |  |  | phone | the address's phone |
| `{prefix}{site}wc_order_operational_data` | `order_key` |  |  | keep | the order key in customer links; not personal and links keep working |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_email` |  | email | the order's billing email |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_phone` |  | phone | the order's billing phone |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_first_name` |  | first_name | the order's billing first name |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_last_name` |  | last_name | the order's billing last name |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_company` |  | company | the order's billing company |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_address_1` |  | street | the order's billing street |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_address_2` |  | street | the order's billing second address line |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_city` |  | city | the order's billing city |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_postcode` |  | postcode | the order's billing postcode |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_country` |  | blank | the order's billing country |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_state` |  | blank | the order's billing state |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_address_index` |  | text | the billing search index; the hpos-address-index row fix rebuilds it from the fake parts |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_phone` |  | phone | the order's shipping phone |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_first_name` |  | first_name | the order's shipping first name |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_last_name` |  | last_name | the order's shipping last name |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_company` |  | company | the order's shipping company |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_address_1` |  | street | the order's shipping street |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_address_2` |  | street | the order's shipping second address line |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_city` |  | city | the order's shipping city |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_postcode` |  | postcode | the order's shipping postcode |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_country` |  | blank | the order's shipping country |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_state` |  | blank | the order's shipping state |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_shipping_address_index` |  | text | the shipping search index; the hpos-address-index row fix rebuilds it from the fake parts |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_customer_ip_address` |  | ip | the IP address the order came from |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_customer_user_agent` |  | user_agent | the browser the order came from |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_stripe_customer_id` |  | blank | the Stripe customer ID, which reaches the live Stripe account |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_stripe_source_id` |  | blank | the Stripe payment source ID |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_stripe_intent_id` |  | blank | the Stripe payment intent ID |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_payment_tokens` |  | blank | saved payment token IDs |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_billing_vat_number` |  | blank | the customer's VAT number |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_wcpay_*customer*` |  | blank | WooPayments customer IDs |

#### `woocommerce-subscriptions` (WooCommerce Subscriptions)

- Stack: wordpress. Checked against: `docs: https://woocommerce.com/document/subscriptions-handles-staging-sites/`.
- Detected by: option `wc_subscriptions_siteurl`, option `woocommerce_subscriptions_is_active`, file `wp-content/plugins/woocommerce-subscriptions/woocommerce-subscriptions.php`, file `wp-content/plugins/woocommerce-subscriptions-core/woocommerce-subscriptions-core.php`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}postmeta` | `meta_value` | `meta_key=_payment_method_token` |  | blank | the token renewals charge; blank so the copy can't renew against the live gateway |
| `{prefix}{site}wc_orders_meta` | `meta_value` | `meta_key=_payment_method_token` |  | blank | the renewal token on an HPOS subscription |
| `{prefix}{site}posts` | `post_excerpt` | `post_type=shop_subscription` |  | text | the customer's note on a subscription kept as a post; free text |
| `{prefix}{site}options` | `option_value` | `option_name=wc_subscriptions_siteurl` |  | keep | the obscured clone of siteurl; left as it is so the copy sees a duplicate site |

#### `wordpress` (WordPress core)

- Stack: wordpress. Checked against: `docs: https://raw.githubusercontent.com/WordPress/WordPress/6.8-branch/wp-admin/includes/schema.php`.
- Detected by: table `{prefix}options`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}users` | `user_email` |  |  | email | the account's email address |
| `{prefix}users` | `user_login` |  |  | username | the login name, often the person's name or email |
| `{prefix}users` | `user_nicename` |  |  | nicename | the URL slug of the login, shown in author links |
| `{prefix}users` | `display_name` |  |  | full_name | the name shown on posts and comments |
| `{prefix}users` | `user_url` |  |  | url | the person's own website |
| `{prefix}users` | `user_pass` |  |  | reset-password | the password hash; reset so no real password works on the copy |
| `{prefix}users` | `user_activation_key` |  |  | blank | a pending password-reset key |
| `{prefix}usermeta` | `meta_value` | `meta_key=first_name` |  | first_name | the first name of the profile |
| `{prefix}usermeta` | `meta_value` | `meta_key=last_name` |  | last_name | the last name of the profile |
| `{prefix}usermeta` | `meta_value` | `meta_key=nickname` |  | username | the nickname, the login unless the person changed it |
| `{prefix}usermeta` | `meta_value` | `meta_key=description` |  | text | the biography, free text |
| `{prefix}usermeta` | `meta_value` | `meta_key=session_tokens` |  | delete-row | login sessions with IP addresses and browsers; nobody needs them on a copy |
| `{prefix}usermeta` | `meta_value` | `meta_key=_application_passwords` |  | delete-row | application passwords that would log in to the copy |
| `{prefix}usermeta` | `meta_value` | `meta_key=wc_last_active` |  | keep | a timestamp, kept for reports |
| `{prefix}usermeta` | `meta_value` | `meta_key=community-events-location` | `serialized:ip` | ip | the dashboard events widget stores the visitor's IP address |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_email` |  | email | the billing email of the account |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_phone` |  | phone | the billing phone of the account |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_first_name` |  | first_name | the billing first name |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_last_name` |  | last_name | the billing last name |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_company` |  | company | the billing company |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_address_1` |  | street | the billing street |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_address_2` |  | street | the second billing address line |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_city` |  | city | the billing city |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_postcode` |  | postcode | the billing postcode |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_country` |  | blank | the billing country, kept by default for tax rules |
| `{prefix}usermeta` | `meta_value` | `meta_key=billing_state` |  | blank | the billing state, kept by default for tax rules |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_phone` |  | phone | the shipping phone of the account |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_first_name` |  | first_name | the shipping first name |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_last_name` |  | last_name | the shipping last name |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_company` |  | company | the shipping company |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_address_1` |  | street | the shipping street |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_address_2` |  | street | the second shipping address line |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_city` |  | city | the shipping city |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_postcode` |  | postcode | the shipping postcode |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_country` |  | blank | the shipping country, kept by default for shipping rules |
| `{prefix}usermeta` | `meta_value` | `meta_key=shipping_state` |  | blank | the shipping state, kept by default for shipping rules |
| `{prefix}{site}comments` | `comment_author` |  |  | full_name | the commenter's name |
| `{prefix}{site}comments` | `comment_author_email` |  |  | email | the commenter's email |
| `{prefix}{site}comments` | `comment_author_url` |  |  | url | the commenter's website |
| `{prefix}{site}comments` | `comment_author_IP` |  |  | ip | the commenter's IP address |
| `{prefix}{site}comments` | `comment_agent` |  |  | user_agent | the commenter's browser |
| `{prefix}{site}comments` | `comment_content` |  |  | text | comment and order note text; emails, phones and known names in it are replaced |
| `{prefix}{site}commentmeta` | `meta_value` |  |  | text | comment metadata of any plugin, scrubbed as free text |
| `{prefix}{site}posts` | `post_title` | `post_type=user_request` |  | email | the email of a personal data export or erasure request |
| `{prefix}{site}posts` | `post_content` | `post_type=user_request` |  | text | the request's extra data as JSON; free text |
| `{prefix}{site}options` | `option_value` | `option_name=admin_email` |  | email | the site's admin email; keep_users keeps it when it matches |
| `{prefix}{site}options` | `option_value` | `option_name=new_admin_email` |  | email | an admin email change waiting for confirmation |
| `{prefix}{site}options` | `option_value` | `option_name=siteurl` |  | rewrite-url | the site's own URL, rewritten to the staging URL |
| `{prefix}{site}options` | `option_value` | `option_name=home` |  | rewrite-url | the home URL, rewritten to the staging URL |
| `{prefix}{site}options` | `option_value` | `option_name=wc_subscriptions_siteurl` |  | keep | never rewritten: the mismatch is what puts WooCommerce Subscriptions in staging mode |
| `{prefix}{site}options` | `option_value` | `option_name=_transient_*` |  | delete-row | cached values, rebuilt on demand and often holding personal data |
| `{prefix}{site}options` | `option_value` | `option_name=_site_transient_*` |  | delete-row | network-wide cached values |

#### `wp-mail-logging` (WP Mail Logging)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/wp-mail-logging/trunk/src/Model/WPML_Mail.php`.
- Detected by: table `{prefix}{site}wpml_mails`, file `wp-content/plugins/wp-mail-logging/wp-mail-logging.php`.
- Truncated: `{prefix}{site}wpml_mails`.

#### `wp-mail-smtp` (WP Mail SMTP)

- Stack: wordpress. Checked against: `docs: https://wpmailsmtp.com/docs/how-to-set-up-email-logging/`.
- Detected by: table `{prefix}{site}wpmailsmtp_emails_log`, table `{prefix}{site}wpmailsmtp_debug_events`, file `wp-content/plugins/wp-mail-smtp/wp_mail_smtp.php`, file `wp-content/plugins/wp-mail-smtp-pro/wp_mail_smtp.php`.
- Truncated: `{prefix}{site}wpmailsmtp_debug_events`, `{prefix}{site}wpmailsmtp_emails_log`.

#### `wpforms` (WPForms)

- Stack: wordpress. Checked against: `docs: https://plugins.svn.wordpress.org/wpforms-lite/trunk/src/Db/Payments/Payment.php`.
- Detected by: table `{prefix}{site}wpforms_entries`, table `{prefix}{site}wpforms_payments`, table `{prefix}{site}wpforms_tasks_meta`, file `wp-content/plugins/wpforms-lite/wpforms.php`, file `wp-content/plugins/wpforms/wpforms.php`.
- Row fixes: wpforms-field-types.
- Truncated: `{prefix}{site}wpforms_logs`.

| Table | Column | Where | Path | Rule | Why |
|---|---|---|---|---|---|
| `{prefix}{site}wpforms_entries` | `fields` |  |  | text | every field of the entry as JSON; wpforms-field-types fakes typed fields and the rest are scrubbed |
| `{prefix}{site}wpforms_entries` | `meta` |  |  | text | entry metadata as JSON |
| `{prefix}{site}wpforms_entries` | `ip_address` |  |  | ip | the IP address the entry came from |
| `{prefix}{site}wpforms_entries` | `user_agent` |  |  | user_agent | the browser the entry came from |
| `{prefix}{site}wpforms_entries` | `user_uuid` |  |  | blank | a cookie ID that follows the visitor across entries |
| `{prefix}{site}wpforms_entry_fields` | `value` |  |  | text | one field's value; wpforms-field-types fakes typed fields and the rest are scrubbed |
| `{prefix}{site}wpforms_entry_meta` | `data` |  |  | text | entry notes and logs |
| `{prefix}{site}wpforms_payments` | `title` |  |  | full_name | the payer's name shown in the payments list |
| `{prefix}{site}wpforms_payments` | `customer_id` |  |  | blank | the gateway's customer ID, which reaches the live account |
| `{prefix}{site}wpforms_payments` | `transaction_id` |  |  | blank | the gateway's transaction ID |
| `{prefix}{site}wpforms_payments` | `subscription_id` |  |  | blank | the gateway's subscription ID, which a renewal would charge |
| `{prefix}{site}wpforms_payment_meta` | `meta_value` |  |  | text | payment details such as the payer's email and IP |

### Capture script

`profile/testdata/capture/capture.sh` (with `compose.yaml`: mysql:8.4, wordpress:6.8-php8.3-apache on 127.0.0.1:33020, wordpress:cli-2.12-php8.3) installs WordPress, WooCommerce 9.8.0, Subscriptions core and the free form, mail-log and shop plugins at pinned versions (each overridable from the environment, e.g. `CF7_VERSION`), enables HPOS, adds three made-up customers and a multisite blog, then writes `out/full.sql` (git-ignored) and the `--no-data` schema files. Run it with Docker and network access:

```sh
profile/testdata/capture/capture.sh
```

It hasn't run yet: the schema files in `profile/testdata/schemas/` were written from the cited sources. After it runs, commit the new schema files and change each free profile's `checked_against` to `install: <plugin> <version>`.

## 4. The pipeline

### Order of a run (PRD 10.1)

1. The config with the flags over it; the profiles (the cached pack when newer, else the built-in ones).
2. `copy` and `prepare` take the run lock on the target (`plan` writes nothing, so it doesn't). A held lock: `lock: another Roadie run is copying to /var/www/staging (pid 4120, started 14:02). Wait for it, or remove <lock path> if it crashed.` (exit 3)
3. The source opens read-only; the target opens read-only for `plan`. `prepare` (and a `plan` whose source and target are one staging) opens the site once and reads it through its read-only export.
4. The target guards (PRD 10.4). Nothing is written when one refuses.
5. The confirmation (below), before anything is written.
6. `prepare`: the database backup (unless `--no-backup`).
7. Pass 1 (index) and its checks: the uncovered-column stop, the url-map stop and `--site`. A stop here leaves the target untouched: no kill switch step, no `.maintenance`, no sink.
8. The file-level kill switch (wp-config defines, mu-plugins, `.env`) on a site target, before anything is imported.
9. `prepare` on WordPress: `.maintenance` is written (`<?php $upgrading = <unix time>; ?>`), so visitors see WordPress's maintenance page during the import. It is removed after the import, also when the run fails.
10. Pass 2 (transform or import), then the commit.
11. The after-import kill switch steps, `--basic-auth`, then verify in exact mode on what was written, read back from the target. `--soundcheck` runs Soundcheck on `--url` after it.

A run that stops after a kill switch or exposure step changed the target reports the target `mixed` (in `data.target_state` and in the error), not `untouched`, also when no row was imported yet. A step that fails first leaves it `untouched`.

### Two passes, the spool and `--one-pass`

- A dump file is read twice. A stream (stdin, or an ssh or alias site that can stream its export) is copied to the encrypted spool in `<cache>/roadie/spool/` (folder 0700) while pass 1 reads it; pass 2 reads the spool. The spool is removed on success, error and Ctrl-C. The rest of a stream after the last statement goes into the spool only when pass 1 reached the end; a parse error or Ctrl-C in pass 1 stops at once, without reading the rest of an ssh export or waiting for stdin to end. A site or database without a stream is exported once per pass.
- Pass 1 reads the schema, row counts, option names, siteurl and home per blog, multisite domains, HPOS mode, kept users, administrators, form definitions, order address part lengths, the uncovered-column samples and the known-value set for exact-mode verify. The rules are then compiled with what it found. Form entries (Gravity Forms `gf_entry_meta`, WPForms `wpforms_entry_fields` and `wpforms_entries`, Ninja Forms `_field_<N>` post meta) are kept in pass 1 (up to 32 MiB, 64 KiB a value); the ones a field-type rule fakes join the known-value set before it is sealed, whichever comes first in the dump (mysqldump writes `gf_entry_meta` before `gf_form_meta`). Past 32 MiB the rest are only faked in their own rows; their emails and phones are still found by the detectors.
- Pass 2 runs `--jobs` workers (default the CPUs, at most 8) with an ordered writer; output order is input order. The window holds at most 2 x jobs batches and at most 96 MiB of batch memory (rows and values counted as held in memory), or a quarter of `--max-memory` when that is smaller; a batch larger than the window still goes when nothing else is in flight.
- `--one-pass` reads once: rules from every profile of the stack, URLs rewritten only for `production:` and `url_map` entries, no names from the known set in free text. It refuses `keep_users` (`--one-pass can't keep users: a dump has their user meta before the users table. Drop --one-pass or keep_users.`) and `--site` (`--one-pass can't limit the copy to --site: the blogs are known only after the whole dump is read. Drop one of them.`). Its uncovered-column check runs after pass 2 has written: a dump file is removed and the message ends `The target is untouched.` while a site or database target the abort leaves mixed reports `mixed` in `data.target_state` and in the error; its message ends `The target is in a mixed state and may hold the column as it was in production: run the same command again once it has a rule, or without --one-pass to check before writing.` (`the columns as they were` and `they have` for several).
- `--max-memory` sets Go's soft memory limit for the run. Without it the run sets 300 MiB (PRD 22.3), unless `GOMEMLIMIT` already set one; the limit in place before the run is put back after.

### Uncovered columns

A text column no rule covers is scanned in pass 1 (a column with no type, as a data-only dump gives it, counts as text) (up to 1,000 non-empty values, each cut to 1 KiB) and listed when its name or its values look personal (the detect lane's PRD 11.3 rule). `copy` and `prepare` then stop before pass 2 and before the kill switch or `.maintenance` touch the site, exit 1, target untouched:

`uncovered: 2 columns look personal and no rule covers them: wp_loyalty_members.email, wp_loyalty_members.phone. Add them under columns: (rhythm-roadie explain uncovered-column), or --allow-uncovered to copy and list them as findings. The target is untouched.`

Finding `uncovered-column` (medium): `wp_loyalty_members.email looks personal (98% of 1,000 sampled values look like emails or phones) and no rule covers it` or `... looks personal (its name) and no rule covers it`. `plan` never stops for it.

### URLs

- Production's siteurl and home (and Laravel's `APP_URL`) and every `production:` entry map to `--url`, keeping a subfolder of `--url` (`https://shop.com` to `https://staging.shop.test/sub`). Subdomain blogs map by suffix; any other blog host needs `--url-map`: `url-map: shop.de has no staging URL; add --url-map shop.de=shop.staging.shop.test.` (exit 2).
- Host columns (`blogs.domain`, `site.domain`, sitemeta keys with `domain` in the name) get the host only; the network's path columns get the subfolder in front.
- `wc_subscriptions_siteurl` is never rewritten, so Subscriptions knows the copy is a duplicate.
- A bad `--url`: `url "staging.shop.test" isn't a staging URL like https://staging.shop.test` (exit 2).

### Multisite `--site`

`copy --site 2` keeps the main site and blog 2: the tables of every other blog are dropped from the copy and listed with action `drop` in the plan and the report. A blog the network doesn't have: `--site 7: this network has no blog 7. Its blogs are 1, 2 and 3.` (exit 2).

Users are limited to the members of the chosen blogs and the main site (a `<prefix>capabilities` or `<prefix><N>_capabilities` user meta row with a role) plus the network's super admins (`site_admins` in sitemeta) and kept users. Every other user and their user meta rows are dropped; the report counts them as rule `site-users`, action `delete-row`, on `<prefix>users`.`ID` and `<prefix>usermeta`.`user_id`. A dump without a usermeta table keeps every user, since nothing says who belongs where.

### Values

- delete-row drops the row; fake, blank, null (blank when the column is NOT NULL), fixed and password replace the whole value. An empty value is never faked. Such a rule wins over path rules (`json:`, `serialized:`) of other where clauses that also match the row: the whole value is replaced, not only the leaves.
- Every other string goes through the nested reader (serialized PHP and JSON): path rules per leaf, the free-text scrubber for text rules, then the URL rewrite and the live-key scan. A live key becomes `keys.test_keys` or is blanked (`keys.mode`).
- A value shaped like serialized data that doesn't parse and holds personal-looking text is blanked with finding `data.unparseable` (medium); `on_unparseable: fail` stops: `data.unparseable: a value at wp_options widget_contact looks serialized, doesn't parse and holds personal-looking text; on_unparseable: fail stops here. Fix the value, or set on_unparseable: blank to blank such values.` (exit 3).
- Kept users (`keep_users`): their users, usermeta and HPOS orders (with addresses, meta and operational data) stay as they were; finding `info.kept-users`: `1 kept users left as they are (keep_users)`. `--admin-password-env ROADIE_X` sets their password; errors: `--admin-password-env names ADMIN_PW; only ROADIE_ variables are read. Rename it ROADIE_ADMIN_PW.`, `--admin-password-env names ROADIE_X, which isn't set. Set it in the environment, or drop the flag.`, `--admin-password-env sets the password of kept users; keep_users names none.`
- Row fixes: Action Scheduler args (JSON up to 191 bytes in `args`, longer in `extended_args` with its md5 in `args`) and the HPOS address index (`_billing_address_index` and `_shipping_address_index` rebuilt from the fake parts; an index with a part of 254 bytes or more keeps its own text rule).

## 5. `prepare`

- Source and target are the staging; a `source:` in the config (or a `--from` the CLI passes) is never read, so `prepare` can't turn into a copy from production. It needs a site or database: `prepare needs a staging site or database; staging.sql is a dump file. Use rhythm-roadie copy for dump files.` (exit 2).
- Backup first, unless `--no-backup`; the result carries the backup path and the restore command for the screen. A failed backup stops with the target untouched: `backup of /var/www/staging: <why>. Fix it, or pass --no-backup to go on without one`. Its code and exit are the cause's when the site returns a typed error (`ssh`, `wp-cli`, exit 3), otherwise `site-config`, exit 3; `data.error` carries them.
- `.maintenance` around the import on WordPress (section 4).
- `--production-url <url>` joins `production:` for the run.

### `--only <parts>`

`data`, `keys`, `side-effects`, `urls`, `noindex`, comma separated or repeated. An unknown part: `--only nope isn't a part; use data, keys, side-effects, urls or noindex` (exit 2).

| Part | What runs |
|---|---|
| `data` | the profile and `columns:` rules, row fixes, kept users, the uncovered-column stop |
| `keys` | the live-key swap in the data |
| `urls` | the URL rewrite |
| `side-effects` | the kill-switch steps and its data rules |
| `noindex` | the exposure steps (noindex, robots) and `--basic-auth` |

Without `data`, `keys` and `urls`, only the tables the kill switch touches are exported and imported (`<prefix>options`, `<prefix>wc_webhooks`, `<prefix>automatewoo_queue`). On a multisite every blog's copies go with them (`<prefix><N>_options` and so on for each `blog_id` above 1 in `<prefix>blogs`), so the kill switch's data rules reach every blog; a table the site doesn't have is skipped. `--only noindex` imports nothing and makes no backup.

- prepare reads the staging's database once, in pass 1, into the encrypted spool and closes that read before the import opens; pass 2 reads the spool. (Reading the database again while the import replaced its tables made MySQL and MariaDB wait on a metadata lock for ever.)
- The staging's own siteurl is no production URL once production is known (`--production-url` or `production:`) and never one that is `--url`'s host: re-preparing a copy with `--url https://staging.shop.test` maps only production to it. A URL pair the rewrite refuses stops in pass 1, before any file of the target is edited.
- On a single site (no `<prefix>blogs`) and on a site without WooCommerce webhooks or AutomateWoo, `--only side-effects` reads only the tables it has; AutomateWoo is reported `not-present` when its queue table is missing. Over ssh or an alias the whole database is read instead (its tables can't be listed without exporting them).
- The box of a prepare verify didn't follow says what was prepared and why: `DONE   prepared staging.shop.test: noindex; not checked (--only noindex reads no data)`. The Search line is left out when `noindex` isn't a part.

## 6. Confirmation

- A site or database target without a terminal needs `--confirm-target` naming it, compared without case: the host of `--url`, the ssh host or the alias name for a site, the database name for a DSN. prepare also takes the host of the siteurl the staging holds (unless that is production's) and names it first when there is no `--url`. `--yes` alone is refused.
  - none: `prepare-confirm: there's no terminal to confirm on. Pass --confirm-target staging.shop.test (the site's host) to go on.` (`(the database name)` for a DSN; `(the site's path)` for a local folder with no host to go by; exit 2). copy stops with its own code, `confirm-target`, worded the same: `confirm-target: there's no terminal to confirm on. ...`
  - wrong: `prepare-confirm: --confirm-target shop.com doesn't name this target, which is staging.shop.test. Pass --confirm-target staging.shop.test (the site's host) to go on.` (also on a terminal)
- On a terminal, `prepare` always asks (also with `--yes`): `prepare rewrites this site's database while it runs. Visitors see a maintenance page for about a minute. Type the site's host to go on (staging.shop.test):`. Another answer: `prepare-confirm: the answer wasn't staging.shop.test, so nothing was changed.`
- On a terminal, `copy` asks `Replace the database of /var/www/staging? [y/N]` unless `--yes`. No: `confirm-target: /var/www/staging wasn't replaced; nothing was changed.`
- A staging that still calls itself by a URL that may be production's (`target-holds-production`) is asked about once on a terminal: `/var/www/staging calls itself https://shop.com. Is https://shop.com the live site? Type yes to go on:`. `yes` takes that URL as `--production-url` and the guards run again; any other answer, or no terminal, keeps the refusal. The URL comes from the target's own data, so control and format characters (escape sequences, bidi overrides) are removed and it ends at the first space, at most 512 bytes, before the question shows it.
- A dump file or stdout target is never asked about.

## 7. Kill switch, exposure and verify in a run

- `--no-kill-switch`: no step is applied and no data rule joins; verify still checks every step, so side effects FAIL as PRD 6.4 says.
- A step that fails stops the run, exit 1, code `kill-switch`: `side-effect.mail: couldn't turn it off on /var/www/staging (permission denied). Fix that, then: rhythm-roadie prepare /var/www/staging --only side-effects.` A files step failing leaves the database untouched.
- `--basic-auth <user>` (or `basic_auth: true`, user `staging`): the snippets go to `.roadie/basic-auth/` (folder 0700, files 0600); the password is in the result for the screen only, never in a report.
- `--no-verify`: no verify; the result has none. A copy to stdout can't be read back, so it has no verify either.
- `plan` lists each step's planned edits; a step that can't plan shows `manual: couldn't plan this step (<why>)`.
- Report fields: the ruleset, config and Echo connector SHA-256 (the connector only when the kill switch installs it on a WordPress site), the profile pack version, the remote commands every opened site ran, the output's SHA-256 for a dump file plus the Not covered section: PRD 19.1's sentence, `Run rhythm tuner scan <staging dir> for file-level leaks.` for a site target with files and `Add an X-Robots-Tag: noindex middleware; Roadie does not edit Laravel code.` for Laravel (also for `prepare --only noindex`, which reads no data: the stack is the site's own).
- Standalone `verify`: the target is read once read-only to find its stack and profiles (a stdin target goes through the encrypted spool, which verify then reads). With `--from`, production is read once, read-only and never spooled, to fill the known-value set and its wp-config.php and .env pairs: exact mode. Every production host it finds counts as production (siteurl, home, every blog domain), also a blog domain `url_map` doesn't name: verify rewrites nothing, so it never stops on url-map. `verify - --from -`: `verify can't read both the copy and --from from stdin (-); name a file or a site for one of them` (exit 2). A dump no profile knows (a CRM's `customers` table) is verified heuristically as stack `sql` instead of stopping with `stack`; an empty dump stops with `nothing-scanned` (exit 3); the report's `data.stack` names the stack found.
- The result carries the side effects (`data.side_effects`): from each kill-switch step's plan for `plan` and `--no-verify`, from verify's read-back otherwise (side-effect checks only). The plan's SIDE EFFECTS block, the copy screen's `Side effects` line and its `Not stopped` line (PRD 13.5) show them; the `Plan:` line counts the ones present.
- verify's uncovered-column scan leaves out the tables the copy truncated or dropped (Laravel's `sessions`) and, for `prepare --only` without `data`, every table.
- String literals kept in `CREATE TABLE` and `ALTER TABLE` (a column's `DEFAULT`, a `COMMENT`) are scrubbed: a known production value gets its fake, a real-looking email a fake email; other literals stay. verify scans them as values under `<table>.(schema)` (not counted as rows). PostgreSQL's `COMMENT ON` (a table, column, view) is treated the same way, named by its table.
- prepare on a staging that already calls itself by its own URL (`--url`, or a siteurl `--production-url`/`production:` doesn't name) no longer counts that URL as a person's when a user's `user_url` equals it, so its siteurl and home are no `exact.survivors`.
- A kept user's (`keep_users`) values stay as they are; production's URLs in their rows (a `user_url` that is the shop's own) are rewritten to `--url` like everywhere else, so no `url.production` follows.
- Standalone `verify` reports fill `data.profile_pack`, `data.ruleset_sha256` and `data.config_sha256` as a copy's do.
- `verify -` removes its encrypted spool when it ends. A closed stdout (`copy --to - | head`) stops the run as `interrupted: the program reading Roadie's output closed it before the end. Run it again when you're ready.` (exit 130), so the spool and temporary files are removed.
- A signal while `copy --from -` waits on stdin ends the run at once: `interrupted` (exit 130), never a `dump-truncated` box first.

## 8. `update` and the profile pack

- `rhythm-roadie update` downloads `checksums.txt`, `checksums.txt.sigstore.json` and `rhythm-roadie_profiles.tar.gz` from the latest Roadie release, checks the signature the way the binary's is checked (the shared release workflow at refs/heads/master, this repository and the tag), checks the archive's SHA-256 against `checksums.txt` and stores the profiles in `<cache>/roadie/profiles/<version>/`. Older packs are removed.
- The archive may hold only `*.yaml` files, flat or under `profile/builtin/`, each under 1 MiB and 16 MiB in all, each a profile this binary can parse, at most 256. Past 16 MiB: `profile pack 1.0.1: rhythm-roadie_profiles.tar.gz holds over 16 MiB of profiles; nothing was installed`.
- The release's version names the cache folder, so a release whose version isn't a plain release version is refused before anything is downloaded: `profile pack: the release's version "../x" isn't a release version, so nothing was installed`. A link, another folder, a path outside the pack, another file type or a damaged archive refuses the whole pack and nothing is stored.
- Output: `Installed profile pack 1.0.1 (24 profiles).`; when the latest release isn't newer than the built-in profiles: `The built-in profiles (1.0.0) are the newest; nothing to download.`; a pinned older pack: `The built-in profiles (1.0.0) are newer, so Roadie keeps using them.`
- Refusals: `profile pack 1.0.1: the signature over checksums.txt doesn't verify, so nothing was installed: <why>`, `profile pack 1.0.1: rhythm-roadie_profiles.tar.gz has SHA-256 <a> where checksums.txt says <b>; nothing was installed`, `profile pack 1.0.1: <file> doesn't parse (it may need a newer rhythm-roadie): <why>; nothing was installed`.
- `update --pin profiles=1.0.1` (and `rhythm lock`) installs that release's pack.
- The pack is used only when its version is newer than the built-in profiles'. The built-in version is the binary's release version (`1.0.0` for a dev build). Its profiles replace the built-in ones with the same ID. Reports record the version in use.
- `--offline` and `RHYTHM_OFFLINE` use the built-in profiles; `update` then prints `RHYTHM_OFFLINE is set: kept the profiles in use (1.0.0).` and downloads nothing.
- `update --clear` removes the cached pack.

## 9. Speed and memory

`BenchmarkCopy200MB` copies a generated 200 MB WooCommerce shop (users, usermeta, HPOS orders and addresses, order notes, Action Scheduler, options with a serialized gateway key) with eight jobs. Until the other lanes merge it runs with their test fakes and from generated batches (no SQL reader), so it measures the engine's own share. `BenchmarkCopy200MBDump` runs the same through a dump file and the real reader once the io lane is in. `ROADIE_BENCH_LARGE=1 go test ./engine -run TestCopyLarge -v` runs 200 MB and 2 GB and logs both.

Measured on 2026-10-26 on an i9-9900K (8 cores, 16 threads) while the other lanes built and tested on the same machine (load average 25 to 114), so the speeds are low bounds, not the PRD 22.3 figures. MB is the generated values' size, which runs about 20% under the asked size.

Before the memory work:

| Run | Load | Index pass | Transform pass | Peak RSS |
|---|---|---|---|---|
| `BenchmarkCopy200MB` | 25 | 44 MB/s | 83 MB/s | 639 MB |
| `TestCopyLarge` 200 MB | 40 | 16 MB/s | 18 MB/s | 702 MB |
| `TestCopyLarge` 2 GB | 40 | 17 MB/s | 31 MB/s | 1,117 MB |
| `BenchmarkCopy200MB` with `--max-memory 300MiB` | 40 | 13 MB/s | 14 MB/s | 320 MB |

After it (byte-bounded window, 300 MiB default soft limit, address lengths in a sorted slice, meta rules found by their where value):

| Run | Load | Index pass | Transform pass | Peak RSS |
|---|---|---|---|---|
| `BenchmarkCopy200MB` | 50 | 36 to 38 MB/s | 37 to 40 MB/s | 302 MB |
| `TestCopyLarge` 200 MB | 69 | 42 MB/s | 33 MB/s | 303 MB |
| `TestCopyLarge` 2 GB | 69 | 19 MB/s | 13 MB/s | 367 MB |
| `TestCopyLarge` 200 MB | 106 | 5 MB/s | 7 MB/s | 301 MB |
| `TestCopyLarge` 2 GB | 106 | 9 MB/s | 6 MB/s | 390 MB |

Memory: the 2 GB run now peaks at 1.21x to 1.30x the 200 MB run (inside PRD 22.3's 1.5x) and the 200 MB run sits at the 300 MB target. What changed: pass 2 counts each batch's memory (a 4 MB batch of short rows holds about 20 MB as one `core.Value` per field) and holds at most 96 MiB in flight; the run sets a 300 MiB soft limit when `--max-memory` and `GOMEMLIMIT` don't set one, so the heap no longer grows to twice what is live; the per-order address lengths are a sorted slice of 24-byte entries (one byte a part) instead of a map, about a third of the memory for the 2.4 million addresses of the 2 GB shop. What still grows with the shop: those address entries (about 57 MB at 2 GB) and the known-value set (8 bytes a value, 600,000 values at 2 GB).

Speed: the CPU profile of the transform lost most of its rule matching (a meta table's value column has a rule per meta key; they are now found by the row's meta key, 5.1 s of CPU down to 0.7 s on the 200 MB run). The index pass shares one goroutine with the generator and the fakes stand in for the reader, the faker and the scrubber, so neither pass can be judged against PRD 22.3 (index 300 MB/s, transform 100 MB/s) until the real reader and faker are in and the machine is quiet. `BenchmarkCopy200MBDump` measures the real reader once the io lane is in.

Generate a dump by hand: `go run ./engine/testdata/gen/dumpgen -size 200MiB -o shop.sql` (made-up people on example.com, phones in the 07700 900 range, IPs in documentation ranges, a live-shaped key assembled at run time).

Determinism: a copy with a stable key gives byte-identical output and identical result JSON (`TestRunDeterminism`, three runs with eight workers; also run with `-count=3`).
