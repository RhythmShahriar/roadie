## Roadie: STOPPED, target-is-production; the target is untouched

> target-is-production: /var/www/shop is shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy.

| | |
|---|---|
| Source | `prod.sql.gz` |
| Target | `/var/www/shop` |
| Copy | https\://staging.shop.test |
| Production | shop.com |
| Stack | not detected |
| Verify | not run |
| Fail on | high |
| Findings | 0 critical · 0 high · 0 medium · 0 low · 0 info |
| Roadie | `1.0.0`, profile pack `-` |

Not covered by this report: files and uploads (PDF invoices, form uploads, images with location data), other databases, object caches (Redis, Memcached), search indexes (Algolia, ElasticPress), logs on disk outside wp-content, copies held by third parties (Mailchimp lists, Stripe customers), names and street addresses written in free text that Roadie didn't see in a structured column, encrypted columns, tables you told Roadie to keep.

Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26).
This report shows what Roadie's checks found in this copy; it is not a statement that
the copy holds no personal data. Signed by roadie:NHUPmL1… (rhythm-roadie report check report.json):
the signature shows who ran Roadie, which version and rules, on which input and that
the report is unchanged since. It doesn't prove the data holds no personal data.

_This view is a rendering of report.json; rhythm-roadie report check report.json checks its signature._
