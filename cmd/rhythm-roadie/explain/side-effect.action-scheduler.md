# side-effect.action-scheduler: Action Scheduler still runs (high)

What: The async runner isn't paused and actions are pending.

Why it matters: WooCommerce, Subscriptions and AutomateWoo run their jobs
here: renewals, follow-up emails, webhooks.

What Roadie does: Roadie's mu-plugin turns the async runner off and takes the
runner off the queue; pending actions are kept (or cancelled with
action_scheduler: cancel).

By hand: Run rhythm-roadie prepare <staging> --only side-effects.

