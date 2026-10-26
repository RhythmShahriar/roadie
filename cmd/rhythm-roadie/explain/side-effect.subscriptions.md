# side-effect.subscriptions: Subscriptions isn't in staging mode (critical)

What: WooCommerce Subscriptions is present and its staging mode isn't forced,
or wc_subscriptions_siteurl matches this site.

Why it matters: Out of staging mode the copy charges renewals and emails
subscribers.

What Roadie does: Roadie's mu-plugin forces staging mode through
woocommerce_subscriptions_is_duplicate_site and leaves
wc_subscriptions_siteurl untouched.

By hand: Run rhythm-roadie prepare <staging> --only side-effects.

