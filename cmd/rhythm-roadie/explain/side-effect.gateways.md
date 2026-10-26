# side-effect.gateways: an enabled gateway isn't in test mode (critical)

What: A payment gateway is enabled and not in test or sandbox mode.

Why it matters: A test order on the copy can charge a real card.

What Roadie does: Known gateways are switched to test mode; enabled gateways
Roadie doesn't know are disabled.

By hand: Switch the gateway to test mode or disable it.

