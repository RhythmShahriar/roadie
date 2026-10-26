# hpos-address-index: an order's address index rebuilt from the fake parts

What: WooCommerce keeps a search index of each order's billing and shipping
address (_billing_address_index, _shipping_address_index). Roadie rebuilds
it from the address's fake parts in the same layout.

Why it matters: An index left as it was holds the real name, street and city
next to the fake address; order search finds the real person.

What Roadie does: copy and prepare rebuild it part by part. An index that
fits no known layout gets the text rule instead.

By hand: Nothing to do. WooCommerce can also rebuild the indexes itself from
the order screen's tools.
