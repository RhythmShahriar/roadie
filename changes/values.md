# Lane values: value codecs and pseudonymisation

What the `codec` and `pseudo` packages change that a user can see: the fake values in a copy, the options that shape them, the passwords, salts and keys Roadie writes, what the free-text scrubber can and can't do, how production URLs are rewritten and how fast all of it runs. Roadie's copy is pseudonymised, not anonymised: whoever holds the consistency key can check a guess.

## Fake values

A fake is `format(kind, HMAC-SHA256(key, kind || 0x00 || normalise(kind, original)))`. The same original gets the same fake in every table, every encoding (serialized, JSON, base64), every worker of one run. It stays the same in every run that uses the same stable key. Another key gives other fakes. A fake is never a plain md5, sha1 or sha256 of the original.

Normalisation before keying, so spellings of one value share a fake:

| Kind | Normalised as | Same fake for |
|---|---|---|
| email | trimmed, lower case | `Jane@X.com`, ` jane@x.com `, `JANE@x.com` |
| phone | digits, a leading `+` kept | `+44 7911 123456`, `+447911123456` |
| names, street, address, city, company | Unicode NFC, case-folded, trimmed, runs of white space made one space | `Zoë` in NFC and NFD |
| ip | canonical form, IPv4-mapped IPv6 unmapped | `::ffff:81.2.69.142`, `81.2.69.142` |
| other kinds | trimmed | |

Formats per kind (the examples come from one test key):

| Kind | Format | Example |
|---|---|---|
| email | `u` + 16 hex + `@` + email_domain | `Jane@Shop.com` to `u517b398146a84734@example.invalid` |
| username | `user_` + 16 hex | `user_af6373ed9df8812e` |
| nicename | the fake login in lower case, at most 50 bytes | |
| first, last, full name | from the locale's lists | `Augustus`, `Robertson`, `Lincoln Rich` (en); `Ilse`, `Moeller`, `Sabina Marquardt` (de) |
| phone | style `echo` (default): `+1 555 01x xxxx`; style `country`: `+44 7700 900xxx` when the original starts with `+44` or `07`, else the echo form | `+1 555 017 3318`, `+44 7700 900352` |
| street, address | house number 1 to 999 and a street from the list, in the locale's order | `829 Lime Grove` (en), `Eisenweg 829` (de) |
| city | from the list | `Guildford` |
| company | a word and a locale suffix | `Apex Ltd`, `Nordsee KG` |
| postcode | the original's shape: letters stay letters, digits stay digits, other bytes kept | `SW1A 1AA` to `FG5M 3DK` |
| dob | moved by up to 180 days either way, inside the same year; a zero date (`0000-00-00`) stays, a value in a date's shape that isn't a real date (`1990-02-29`) keeps its year and gets a valid month and day, any other value that isn't a date gets new digits in the same shape; one with no digit at all is blanked | `1980-05-17` to `1980-11-13` |
| ip | IPv4 into 192.0.2.0/24, 198.51.100.0/24 or 203.0.113.0/24; IPv6 into 2001:db8::/32 | `198.51.100.166`, `2001:db8:71a4:ccce:d3a7:6ff4:f9a3:f897` |
| url | `https://u` + 12 hex + `.example.invalid/`; a narrow column drops the closing slash, never a hex digit | |
| user agent | always `Mozilla/5.0 (Roadie staging copy)` | |
| text | not faked: scrubbed (below) | |

Every format is one `verify` never flags: reserved domains, fictional phone ranges and documentation IP ranges.

### Fitting the column

A fake never exceeds the column's byte limit. Names are cut on a word; unique kinds (email, username) shorten their hex part but never below 12 hex, for example `u517b398146a84@example.invalid` at 30 bytes. A URL keeps all 12 hex and only drops its closing slash, so it needs 36 bytes. A column too narrow is an error. For an email or an email-shaped username:

`a fake <kind> needs at least <n> bytes but the column holds <m>; blank the column or use a shorter pseudonymise.email_domain`

For every other kind (the email domain plays no part):

`a fake <kind> needs at least <n> bytes but the column holds <m>; blank the column`

### Collisions

Two different originals with the same fake in a unique column of one run stop the run with code `fake-collision` (exit 3):

`fake-collision: two different <kinds> got the same fake in this run. Run again (a new key fixes it); this should happen about once in 100 million runs.`

### Options

| Option | Default | Rule | Error (code `config`, exit 2) |
|---|---|---|---|
| `pseudonymise.email_domain` | `example.invalid` | must be reserved: `example.com`, `example.net`, `example.org` or a name ending in `.example`, `.invalid`, `.test`, `.localhost` or `.local` | `pseudonymise.email_domain "<d>" isn't reserved: use example.com, example.net, example.org or a name ending in .example, .invalid, .test, .localhost or .local` |
| `pseudonymise.locale` | `en` | one of `en`, `de`, `fr`, `es`, `nl`, `it` | `pseudonymise.locale "<l>" isn't one of en, de, fr, es, nl, it` |
| `pseudonymise.phone_style` | `echo` | `echo` or `country` | `pseudonymise.phone_style "<s>" isn't echo or country` |

Asking for a fake of kind text is `no fake for the kind "text"; text is scrubbed, not faked`.

### Locales

Each locale ships lists of at least 500 first names, 500 last names, 200 streets, 200 cities and 100 company words, plus street and company suffixes. All entries are ASCII (German, French, Spanish, Dutch and Italian names are transliterated), so a latin1 table can't break.

`IsFake` (used by verify and by the scrubber to leave fakes alone) knows emails, usernames, nicenames and URLs by format and reserved domain; names, streets, addresses, cities and companies by membership of any locale's lists, ignoring case; phones and IPs by their reserved ranges; the user agent by equality. Postcodes, dates of birth and text can't be told apart from real ones and always report false.

## Consistency key

- `per-run` (the default): 32 bytes from the system's random source, in memory only, gone at exit. Fakes differ from run to run.
- `stable`: read from `ROADIE_KEY` or `.roadie/key`, written as standard base64 of 32 bytes with padding. Surrounding spaces and a trailing newline are ignored.
- The key never enters the copy, the plan, the report or a log. The report shows its fingerprint: `roadie-key:` and the first 12 characters of the lower-case unpadded base32 of SHA-256(key), for example `roadie-key:6var5r7fdzdb`.
- Errors (code `config`, exit 2), never quoting the value: `consistency key: not base64 of 32 bytes; make a new one with rhythm-roadie config key rotate` and `consistency key: no random bytes from the system: <err>`.

## Passwords, salts and APP_KEY

- Reset hash (`passwords: reset`, every user not kept):
  - WordPress: `$P$roadie` and 22 random characters of the phpass alphabet (`./0-9A-Za-z`), 31 bytes. phpass reads the `r` as an iteration count out of its range and refuses the hash, so no password ever logs in. Checked against 1,000 passwords with a Go phpass and with PHP running WordPress's CheckPassword.
  - Laravel and sql: bcrypt with cost 10 of 32 random bytes that are thrown away.
- Admin password (`--admin-password-env NAME`, for kept users):
  - WordPress: a phpass portable hash, `$P$B`, an 8-character salt from 6 random bytes and 2^13 md5 rounds, 34 bytes. Every WordPress since 2.5 accepts it, 6.8 and later (bcrypt by default) included; WordPress rehashes it at the first login.
  - Laravel and sql: bcrypt with cost 12.
  - bcrypt hashes are written with the `$2y$` prefix PHP writes. Go's own `$2a$` is the same algorithm; PHP's `password_get_info` doesn't call it bcrypt, so Laravel 11's `Hash::check` would refuse it.
  - Errors (code `config`, exit 2), never quoting the password: `admin password: the variable is empty; set it to the password kept users log in with` and `admin password: longer than 72 bytes, which bcrypt can't hash; use a shorter one` (bcrypt's limit, applied to every stack).
  - A stack other than wordpress, laravel or sql: code `stack` (exit 2), `no password hash for the stack "<s>"; Roadie knows wordpress, laravel and sql`.
- Salts: 64 characters from the system's random source, drawn evenly from the characters WordPress salts use (letters, digits and ``!@#$%^&*()-_ []{}<>~`+=,.;:/?|``). No quote or backslash, so a salt goes into wp-config.php's single-quoted defines as it is.
- APP_KEY: `base64:` and the standard base64 of 32 random bytes, the form `php artisan key:generate` writes for AES-256-CBC.

## The free-text scrubber

What it replaces in a text value (order notes, comments, form messages):

- Every span the detector finds (emails, phones, IPs and the other fake kinds) becomes the fake of that kind, the same fake the structured column got. Cards, IBANs and any span of a kind without a fake become `XXXX`.
- Email addresses found by their `@` and read as Unicode, so `maría.lópez@correo.example`, `jöhn.dœ@exämple-shop.de` and `user+tag@例え.jp` are replaced whole. A trailing `.` or `-` stays outside the address.
- Names seen in a structured column of the same run: a known word pair becomes a two-word fake full name; a pair joined by an apostrophe or hyphen (`O'Sullivan`, `Jean-Luc`) counts as one word and gets a one-word fake; a single known word of 3 or more letters becomes a one-word fake when it starts with a capital letter (`Will`, `MARK`). A pair matches in any case (`mark green`).
- A span that already holds a Roadie fake stays, so scrubbing twice changes nothing more. A capitalised word pair that reads as a Roadie fake full name and isn't a known pair (`Kylie Kim` from an earlier pass) stays whole, even when one word of it (`Kim`) is a known name.
- Text outside the replaced parts is kept byte for byte.

What it can't do:

- A name Roadie never saw in a structured column (a neighbour named in a note) stays. Free text holds names no pattern can find; `free_text: blank` empties such fields instead.
- A known word under 3 letters on its own stays.
- A known name written in lower case on its own stays, so a customer named Will, Mark or Green leaves "we will mark the green box" alone. The cost: "ask bartholomew" keeps the name. A capitalised common word that is a known name ("Will you call?") is still replaced.
- A single list name standing alone (`Kylie`) that is also a known real name is replaced again on a second pass: it can't be told apart from the real one. A pair Roadie never saw that happens to read as a fake full name keeps a known word inside it.
- Addresses, streets and postcodes typed into free text are not recognised unless the detector reports them as spans.

## Production URLs

Every form below is rewritten from a production URL to the staging one:

- `https://host`, `http://host` and `//host`
- JSON escapes: `https:\/\/host` and `\/\/host`, with any number of escaping backslashes (double-encoded JSON too)
- URL-encoded: `https%3A%2F%2Fhost` and `%2F%2Fhost`, hex in any case
- HTML entities: `&#47;`, `&#x2F;`, `&#X2f;` with any leading zeros, `&sol;` and `&colon;`; spellings may mix
- inside PHP serialized values, JSON and base64-serialized values at any depth, with every serialized length fixed

The separators keep their spelling; the staging path is written in the spelling of the slashes before the host. Both schemes of a production URL take the staging scheme; a bare-host staging entry keeps the text's scheme. The `www.` variant of each production host is added in both directions unless named in a pair. IDN hosts match in punycode and in their Unicode spelling. The longest production URL wins.

Boundary rule (what stays):

- The host must follow two slashes in some spelling, so `jane@shop.com`, `https://jane@shop.com`, `myshop.com` and bare `shop.com` in prose stay.
- A host that is the user part of a URL to another host stays: in `https://shop.com@other.example/` (or `shop.com:pw@`, or `%40` for the `@`) the URL points at other.example, so it isn't rewritten.
- The byte after the host must not be a letter, digit or `-`, nor a `.` followed by a letter or digit, so `shop.com.au`, `shop.community` and `shop.com-x.net` stay. A dot spelled `%2E` (any case), `&#46;`, `&#x2E;` or `&period;` counts as a dot, so `shop.com%2Eau` and `shop.com&#46;au` stay too.
- A production URL with a path only matches before a byte outside `[A-Za-z0-9._~-]`: `/blog` doesn't match `/blogger`.
- A port in the text stays (`shop.com:8443` becomes `staging.shop.test:8443`) unless the staging URL names one. Its colon may be spelled `:`, `%3A` (any case), `&#58;`, `&#x3A;` or `&colon;`, so `https%3A%2F%2Fshop.com%3A8443%2F` keeps or drops its port like the raw form. A port the staging URL names is written with a plain `:`. A production URL with a port matches only that port, in any of these spellings.
- Subdomains (`fr.shop.com`) are rewritten only with a pair of their own.
- Host case is ignored (ASCII).

Known limits (what stays that a reader might expect rewritten):

- A production URL with user info (`https://admin@shop.com/wp`) stays: the host doesn't follow two slashes. Such URLs are rare in a site's own data and the rule keeps `https://jane@shop.com`-shaped text in prose from being half rewritten.
- A slash spelled as a JSON Unicode escape (`https:\u002F\u002Fshop.com`) stays in raw text. Inside a JSON value the nested decoder turns `\u002F` into `/` first, so the URL is rewritten there.

URL map errors (code `config`, exit 2): `url map: "<entry>" isn't a URL or a host like shop.com or https://shop.com/path`, `url map: "https://***@staging.shop.com" has user info, which isn't allowed; write a URL or a host like shop.com or https://shop.com/path` (everything between the scheme and the last `@` is shown as `***`, so a password never reaches the message) and `url map: <host> maps to itself; the staging URL needs a host of its own`.

## PHP serialized values

Read with PHP 8's grammar, checked against PHP's own unserialize (8.1 and 8.3) on millions of fuzzed inputs. A value that isn't valid serialized data is treated as text: `not valid PHP serialized data at byte <N>: <reason>`. Differences from PHP, on purpose: bytes after the value make it invalid, the old `S:` escaped string is invalid, enums are accepted without their class, nesting stops at 512 levels and `:{` must follow an object's count as PHP 8.3 requires. Untouched bytes are copied as they were; only a changed string gets a new length.

Nested values (a serialized array inside JSON inside a column, say) are opened up to 16 format levels deep and within 64 times the value's size of work; a value past either limit is handled as text.

Only string values are rewritten. JSON object keys and serialized array keys are kept as they are, so an email used as a key (`{"jane@shop.com": {...}}`) stays in the copy unless the caller handles it.

## Speed

Measured on an i9-9900K shared with other work (load average 30 to 55 on 16 threads), one CPU per benchmark, best of three; a quiet machine is faster.

| Benchmark | Measured | Target |
|---|---|---|
| BenchmarkRewrite (PHP serialized, 1,000 records) | 230 MB/s (250 MB/s at load 8) | 120 MB/s |
| BenchmarkValid (PHP serialized) | 620 MB/s | |
| BenchmarkNested (JSON rows holding serialized data) | 54 MB/s (77 MB/s at load 8) | |
| BenchmarkURLRewrite, prose with no match | 1,590 MB/s (2,830 MB/s at load 8) | 300 MB/s |
| BenchmarkURLRewrite, text dense with URLs | 185 MB/s (228 MB/s at load 8) | |
| BenchmarkFake, cached email | 120 to 150 ns at load 30 | 300 ns |
| BenchmarkFake, cached other kinds | 95 to 310 ns | |
| BenchmarkFake, uncached email (first sight, cache still growing) | 1.08 us at load 30, 600 bytes and 3 allocations | 1.5 us |
| BenchmarkFake, email miss in a full cache | 0.76 to 1.1 us at load 30, one 48-byte allocation | |
| BenchmarkFake, uncached name, phone, postcode, ip | 1.0 to 1.6 us | |
| BenchmarkScrub, notes with nothing to replace | 26 MB/s | |
| BenchmarkScrub, notes with a name and an email per sentence | 19 MB/s | |

Guards that run with every test (the allocation counts skip themselves under the race detector, which adds its own; `go test ./...` without `-race` runs them): no `regexp` in any non-test source of `codec` or `pseudo`; checking a PHP serialized value and rewriting text with no production URL allocate nothing; a rewrite that changes nothing allocates only for leaf paths; a cached fake or a miss in a full cache allocates only its result; scrubbing text with nothing to replace costs the same few allocations at any length. Known, the set of values seen in structured columns that the scrubber checks words against, holds 2 million emails in 52 MB. 2 million full names (2,000 first names and 100,003 last names) take at most 55 MB of Go heap while they are added and 53 MB once sealed; Seal itself allocates under 1 MB: entries are 8 bytes (a 56-bit keyed hash and a kind byte), every 524,288 entries are sorted in place and repeats dropped; Seal sorts the chunks where they are instead of copying them. TestKnownHeap measures the heap itself; TestKnownMemory and TestKnownMemoryNames count the tables and filters, also under the race detector. CI runs every benchmark once (`-benchtime 1x`) so a broken one fails the build.
