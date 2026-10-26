# side-effect.not-applicable: the target is a database only (info)

What: The target is a DSN: there are no config files to edit.

Why it matters: Roadie can't install the Echo seal or pause cron on a database
alone.

What Roadie does: Data rules still apply: webhooks paused, gateway options to
test mode.

By hand: Turn mail and jobs off where the app runs.

