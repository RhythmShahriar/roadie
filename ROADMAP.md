# Roadmap

Roadie 1.0 leaves these out on purpose. Each comes later; `plan` names the first three when they apply to your site.

## Not in 1.0

- **Files and uploads.** Roadie copies the database only; the plan prints an rsync exclude list for the folders that hold personal data.
- **Web server config.** Roadie never edits it; basic auth is a printed snippet for Apache, nginx and Caddy.
- **Object caches and search indexes.** Redis, Memcached, Algolia and ElasticPress are neither copied nor cleared.
- **Fleet runs and scheduled refresh (planned for 1.1).** One `sites.yaml` for many sites, with notifications; 1.0 has the lock file and the Action's nightly example.
- **mydumper folders.** Export with `mysqldump` or `mariadb-dump` instead.
- **`pg_dump -Fc` and `-Fd` archives.** Export with `pg_dump -Fp` (plain SQL) instead.
- **Binary backups.** xtrabackup and other physical backups can't be read.
- **`.wpress` and Duplicator archives.** Refused with the export command to use instead.
- **Drupal, Magento, Shopify and MongoDB.** Generic SQL with a `columns:` config covers other SQL apps today.
- **An in-place `UPDATE` mode for `prepare`.** 1.0 exports, pseudonymises and imports the whole database.
- **`--jsonl` progress events.** 1.0 has the JSON report at the end of a run.
- **Keyless report signing.** Reports are signed with a local Ed25519 key in 1.0; signing with cosign through a CI identity comes later.

## Next

In the order they are planned. None has a date; each ships when it is tested on the testbed.

1. **Fleet runs and scheduled refresh (1.1).** One `sites.yaml` for many sites, one report per site and a summary, with notifications when a refresh fails.
2. **`--jsonl` progress events.** One JSON line per phase and table on stderr, for CI dashboards that want more than the final report.
3. **Speed on large dumps.** The benchmark's targets are measured on a shared machine so far; a run on a quiet one comes first, then work on what it shows.
4. **`demo --keep <dir>`.** Keep the demo's files in a folder you name, not only in the cache.
5. **Keyless report signing.** Sign with cosign through the CI job's identity, so a report names the workflow that made it.

Ideas outside this list are welcome as issues; a use case helps more than a feature name.
