# side-effect.guard-changed: Roadie's guard plugin was changed (high)

What: wp-content/mu-plugins/roadie-staging.php differs from the file Roadie
wrote. Its header holds the sha256 of the file and the bytes no longer
match it.

Why it matters: The guard pauses Action Scheduler, keeps Subscriptions from
charging renewals, puts Jetpack offline, blocks wp_mail without Echo and adds
noindex. A hook commented out or put in dead code still has its name in the
file but no longer runs, so verify can't tell which of them still hold.
Verify then reads every hook of a changed guard as missing.

What Roadie does: prepare writes the guard again.

By hand: Run rhythm-roadie prepare <staging> --only side-effects. To change
what the guard does, use side_effects: in .roadie/config.yaml instead of
editing the file.
