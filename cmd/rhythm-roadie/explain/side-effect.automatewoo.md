# side-effect.automatewoo: AutomateWoo's queue holds jobs (high)

What: automatewoo_queue has rows.

Why it matters: Queued workflows send emails and SMS to customers.

What Roadie does: Roadie truncates the queue; it runs through Action
Scheduler, which is paused too.

By hand: Empty the queue in AutomateWoo > Queue.

