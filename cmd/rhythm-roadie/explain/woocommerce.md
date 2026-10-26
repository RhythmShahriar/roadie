# woocommerce: the woocommerce profile

What: WooCommerce legacy orders in posts and postmeta (_billing_* and
_shipping_*), customer lookup, sessions and payment tokens truncated, download
logs, webhooks paused and API keys truncated.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles woocommerce lists every table, column and rule with what it was
checked against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

