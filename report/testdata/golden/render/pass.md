## Roadie: PASS, staging.shop.test is a pseudonymised copy

| | |
|---|---|
| Source | `ssh://deploy@shop.com/var/www/shop` |
| Target | `/var/www/staging` |
| Copy | https\://staging.shop.test |
| Production | shop.com |
| Stack | wordpress \(wordpress, woocommerce, woocommerce-hpos\) |
| Verify | exact, every row of 61 tables |
| Fail on | high |
| Findings | 0 critical · 0 high · 0 medium · 0 low · 1 info |
| Roadie | `1.0.0`, profile pack `1.0.0` |

| Check | Result |
|---|---|
| No pseudonymised value survives | ✅ 1,210,044 checked |
| No real-looking emails | ✅ 18,402 checked, all \@example.invalid or kept |
| No live keys | ✅ database, wp-config.php |
| Side effects off | ✅ 2 of 2 |

### Findings

- **INFO** `info.kept-users` 1 user kept as it is: wp\_users.user\_email 1

<details><summary>What changed (3 tables, 1 key, 3 side effects)</summary>

| Table | Rows | What |
|---|---|---|
| `wp_users` | 18,402 | user\_email \(wordpress.users.user\_email\) 18,401 changed, 1 kept; user\_pass \(wordpress.users.user\_pass\) 18,401 changed, 1 kept |
| `wp_wc_orders` | 41,077 | billing\_email \(woocommerce.wc\_orders.billing\_email\) 41,077 changed |
| `wp_woocommerce_sessions` | 912 | truncate |

| Key | Where | Value | Action |
|---|---|---|---|
| Stripe live | wp\_options woocommerce\_stripe\_settings → serialized:secret\_key | `sk_l•••••` | swapped |

| Side effect | Status | How | To turn it back on for a test |
|---|---|---|---|
| Echo connector, sealed | off | Echo connector sealed in wp-content/mu-plugins | rhythm echo up on the staging host |
| WP-Cron | off | DISABLE\_WP\_CRON in wp-config.php | wp cron event run --due-now |
| Jetpack | not on this site |  |  |

Not stopped: a server crontab calling wp cron or artisan schedule:run, wp action-scheduler run by hand, queue workers already running, code that opens SMTP sockets or uses fsockopen with credentials Roadie doesn't know, raw cURL without --echo-proxy. Credentials are blanked so those paths fail at the provider. Blocking outbound traffic at the host is the only certainty.

</details>

Not covered by this report: files and uploads (PDF invoices, form uploads, images with location data), other databases, object caches (Redis, Memcached), search indexes (Algolia, ElasticPress), logs on disk outside wp-content, copies held by third parties (Mailchimp lists, Stripe customers), names and street addresses written in free text that Roadie didn't see in a structured column, encrypted columns, tables you told Roadie to keep.

Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26).
This report shows what Roadie's checks found in this copy; it is not a statement that
the copy holds no personal data. Signed by roadie:NHUPmL1… (rhythm-roadie report check report.json):
the signature shows who ran Roadie, which version and rules, on which input and that
the report is unchanged since. It doesn't prove the data holds no personal data.

_This view is a rendering of report.json; rhythm-roadie report check report.json checks its signature._
