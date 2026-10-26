# killswitch: the kill switch's database rules

What: The rules the kill switch adds to the data pass. Rules on an option
are named killswitch.options.option_value.<option>.<field>: payment gateways
put in test mode (an unknown gateway disabled) and their live keys swapped
for test keys, mail plugins set to the default PHP mailer with their
credentials blanked and Jetpack's connection tokens blanked.
killswitch.wc_webhooks.status pauses WooCommerce webhooks and
killswitch.wc_webhooks.secret blanks their signing secrets. AutomateWoo's
queue and a Laravel app's jobs and job_batches are dropped;
side_effects.action_scheduler: cancel cancels pending scheduled actions.

Why it matters: These are what a staging copy would otherwise do to the real
world: mail customers, charge cards, call webhooks.

What Roadie does: copy and prepare apply them unless --no-kill-switch or a
side_effects: setting is keep (side_effects.gateways: keep,
side_effects.mail: keep, side_effects.webhooks: keep); the SIDE EFFECTS
block and the side-effect findings say what is off.

By hand: rhythm-roadie prepare <staging> --only side-effects applies them to
a copy made without them.
