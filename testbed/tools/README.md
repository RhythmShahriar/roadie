# The other tools, judged honestly

The benchmark (`go run -tags testbed ./testbed/bench`) runs other tools on the same seeded inputs as Roadie after the scenarios and records them in `results.json` under `tools`. Each tool is judged only on what it sets out to do. Anything else is "can't see" for it, never "missed". A tool that can't run here is "not run" with the reason, never a guess.

Versions are pinned in `testbed/Makefile` (`TOOLS`); `make -C testbed tools` (run by `make up`) fetches them into `runtime/tools/<name>/` and checks each sha256. The configuration each tool runs with is in this folder, so anyone can improve it and rerun the benchmark.

| Tool | Version | Inputs | Judged on | Can't see |
|---|---|---|---|---|
| WP-CLI `search-replace` | 2.12.0, in the pinned `sshd` image | r01, r09, r12 | production URLs left in the database; lookalike links, other subdomains and addresses at the production domain it changed | personal values, live keys, side effects |
| Replibyte | 0.10.0 | r01, r12 | seeded personal values and live keys left (the oracle), serialized values that no longer unserialize (real PHP) | URLs, side effects |
| Greenmask | 0.2.26 | r01, r12 | as Replibyte | URLs, side effects |
| Staging Shield (wordpress.org) | 1.0.2 | r01, r13 | side effects: sinkhole connections, stub calls with a live key and mail captured to real recipients during the side-effect drill | personal values, live keys, URLs |

## How each one runs

- **WP-CLI** (`wp-cli/search-replace.txt`): the input is loaded into a scratch database `tools` on prod-db (MySQL 8.4) and every pair in the file runs in order as `wp search-replace <from> <to> --all-tables --precise --skip-plugins --skip-themes`, from the sshd container. The result is dumped with `mysqldump`. A production URL is the host after a scheme, `//`, `\/\/`, an encoded slash or `www.`; `shop.example.com.au`, `myshop.example.com`, `mg.shop.example.com` and `info@shop.example.com` must stay as they are. A plain `//shop.example.com` pair also rewrites `//shop.example.com.au`: that is counted, as it would break the lookalike link on a real site.
- **Replibyte** (`replibyte/conf.yaml`): the fetched binary runs in the testbed's runner image with no network and telemetry off (`-n`): `dump create -s mysql -i` reads the input on stdin into a local datastore, then `dump restore local -v latest -o` writes the dump the oracle reads. Replibyte 0.10.0 panics on an INSERT that doesn't name its columns and keeps only the first row of one that holds several, which is how mysqldump writes by default. So it gets the same rows one INSERT each with their columns named, as `mysqldump --complete-insert --skip-extended-insert` writes them: it is judged on its masking, not on the dump's style. Replibyte transforms whole columns by name, so columns that mix personal values with settings (`wp_usermeta`, `wp_wc_orders_meta`, `wp_options`) are left alone; a column-wide transformer there would break every setting in them.
- **Greenmask** is not run: its pinned release dumps and restores PostgreSQL only; the inputs it would meet are MySQL dumps of WordPress shops. When a release reads MySQL, pin it in the Makefile and add its configuration here.
- **Staging Shield**: staging gets the raw input (a copy made without Roadie), the plugin is installed and activated as it comes, then the traffic generator and the side-effect drill run for the drill's length and gate 3 is read.

## Making a tool run

Replibyte's pinned sha256 is the one its release publishes (the `.sha256sum` asset), checked against the download. wordpress.org publishes no checksum for a plugin, so Staging Shield's is that of the zip as downloaded (twice, the same bytes). A tool whose line in `TOOLS` says `unverified` is never downloaded: its sha256 hasn't been checked yet. To enable it, download the release on a machine you trust, check it against the project's published checksum or signature, put its sha256 in the line, then `make -C testbed tools`. A failed download or a different sha256 gives "not run" with that reason.
