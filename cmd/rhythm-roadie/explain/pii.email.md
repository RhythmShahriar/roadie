# pii.email: a real-looking email address (high)

What: An email address whose domain isn't a reserved one (example.com,
.invalid, .test and the rest of RFC 2606 and 6761). HTML-entity, URL-encoded
and JSON forms count too.

Why it matters: An email is enough to reach and identify a person. A staging
copy that keeps it is still processing personal data and can mail real
customers.

What Roadie does: copy and prepare replace every email in a covered column
with u<16 hex>@example.invalid and scrub free text. Allowed addresses
(verify.allow_emails) and kept users are left as they are.

By hand: Add the column under columns: with rule: email, or allow a business
address with --allow-emails.

