# uploads.personal: files with personal data in the site's folders (critical to medium)

What: Backups (critical), logs (high) or form uploads (medium) in wp-content:
debug.log, uploads/wc-logs, uploads/gravity_forms, updraft, ai1wm-backups, or
.sql, .sql.gz and .zip files in the web root.

Why it matters: Roadie copies no files, so whatever was copied with the site
keeps its real data.

What Roadie does: verify lists them by name and size; it never opens archives.

By hand: Delete them from the copy, or leave the folders out when you copy
files (plan prints an rsync exclude list).

