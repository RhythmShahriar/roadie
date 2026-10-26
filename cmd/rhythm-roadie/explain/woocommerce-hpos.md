# woocommerce-hpos: the woocommerce-hpos profile

What: WooCommerce HPOS tables: wc_orders, wc_order_addresses, wc_orders_meta
and wc_order_operational_data, with the address indexes recomputed from the
fake parts.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles woocommerce-hpos lists every table, column and rule with what it was
checked against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

