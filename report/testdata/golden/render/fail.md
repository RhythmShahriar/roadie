## Roadie: FAIL, staging.sql.gz is not a safe copy yet

| | |
|---|---|
| Target | `staging.sql.gz` |
| Production | shop.com |
| Stack | wordpress \(wordpress, woocommerce\) |
| Verify | heuristic, every row of 58 of 61 tables |
| Fail on | high |
| Findings | 1 critical · 1 high · 1 medium · 0 low · 0 info |
| Roadie | `1.0.0`, profile pack `-` |

| Check | Result |
|---|---|
| No real-looking emails | ❌ 3 real-looking emails |
| No real-looking phones | ✅ 41,077 checked |
| No live keys | ❌ Stripe live key |
| No personal files in uploads | ➖ a dump has no uploads |

### Findings

- **CRITICAL** `key.stripe-live` Stripe live key: wp\_options.option\_value option\_name=woocommerce\_stripe\_settings → serialized:secret\_key 1 (e.g. `sk_l•••••`)
- **HIGH** `pii.email` 3 real-looking emails: wp\_users.user\_email ID=42 2 · wp\_postmeta.meta\_value meta\_key=\_billing\_email 1 (e.g. `ad•••@gm•••.com`). Fix: `rhythm-roadie prepare staging.sql.gz`
- **MEDIUM** `config.environment` WP\_ENVIRONMENT\_TYPE is unset: wp-config.php line 41 1
- `wp_loyalty_members.email` looks personal (92% of sampled values) and no rule covers it

<details><summary>What changed (1 key)</summary>

| Key | Where | Value | Action |
|---|---|---|---|
| Stripe live | wp\_options woocommerce\_stripe\_settings → serialized:secret\_key | `sk_l•••••` | kept |

</details>

Not covered by this report: files and uploads (PDF invoices, form uploads, images with location data), other databases, object caches (Redis, Memcached), search indexes (Algolia, ElasticPress), logs on disk outside wp-content, copies held by third parties (Mailchimp lists, Stripe customers), names and street addresses written in free text that Roadie didn't see in a structured column, encrypted columns, tables you told Roadie to keep, uploads on the staging host.

Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26).
This report shows what Roadie's checks found in this copy; it is not a statement that
the copy holds no personal data. Signed by roadie:NHUPmL1… (rhythm-roadie report check report.json):
the signature shows who ran Roadie, which version and rules, on which input and that
the report is unchanged since. It doesn't prove the data holds no personal data.

_This view is a rendering of report.json; rhythm-roadie report check report.json checks its signature._
