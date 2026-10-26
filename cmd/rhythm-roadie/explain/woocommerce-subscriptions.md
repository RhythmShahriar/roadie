# woocommerce-subscriptions: the woocommerce-subscriptions profile

What: WooCommerce Subscriptions: subscription orders by the order rules,
gateway customer and token IDs blanked, wc_subscriptions_siteurl never
rewritten, staging mode forced.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles woocommerce-subscriptions lists every table, column and rule with
what it was checked against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

