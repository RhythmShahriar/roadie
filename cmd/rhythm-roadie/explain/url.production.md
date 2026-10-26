# url.production: a production URL left in the copy (medium)

What: Production's host in any form (https, http, protocol-relative, www,
JSON-escaped, URL-encoded, inside serialized or JSON data).

Why it matters: Links on the copy send testers to production; forms and
redirects can post there.

What Roadie does: copy and prepare rewrite every form to the staging URL,
longest match first, with serialized lengths fixed.

By hand: Add the host to url_map if it is a subsite or CDN, then copy again.

