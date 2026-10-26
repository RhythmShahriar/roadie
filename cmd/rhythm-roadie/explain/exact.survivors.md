# exact.survivors: a production value survived the copy (critical)

What: In exact mode Roadie keeps a keyed hash of every structured personal
value it replaced in production. This finding means one of them is still
somewhere in the copy, in raw, lower-case, JSON-escaped, URL-encoded,
HTML-entity, base64, md5 or sha256 form. The detail names the encoding.

Why it matters: A survivor is a real person's data that the rules missed,
often in a column or nested value no profile knows, or in a hash a plugin
stored as a cache key.

What Roadie does: Every value of a known kind is replaced through one mapping
per kind, so a survivor points at a place no rule covers yet.

By hand: Find the table and column in the finding, add a rule for it under
columns: in .roadie/config.yaml and copy again. rhythm-roadie plan -v shows
which rules cover each column.

