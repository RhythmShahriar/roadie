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
