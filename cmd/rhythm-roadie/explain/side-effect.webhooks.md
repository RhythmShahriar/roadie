# side-effect.webhooks: a WooCommerce webhook is active (high)

What: A wc_webhooks row is active.

Why it matters: Webhooks post orders and customers to real services such as
Zapier or a fulfilment partner.

What Roadie does: Roadie sets each webhook to paused and blanks its secret;
the delivery URL is kept.

By hand: Pause the webhook in WooCommerce > Settings > Advanced > Webhooks.

