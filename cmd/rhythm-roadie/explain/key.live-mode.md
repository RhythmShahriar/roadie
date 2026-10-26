# key.live-mode: a payment gateway in live mode (high)

What: A gateway's settings say live mode (testmode = no, sandbox off,
WooPayments live, PayPal environment live), even with blank keys.

Why it matters: A copy in live mode can charge real cards once someone adds a
key; it also confuses everyone testing it.

What Roadie does: copy and prepare switch known gateways to test or sandbox
mode and disable unknown enabled gateways.

By hand: Switch the gateway to test mode in WooCommerce, or run rhythm-roadie
prepare <staging> --only keys.

