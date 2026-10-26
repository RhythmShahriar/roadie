# action-scheduler: the action-scheduler profile

What: Action Scheduler: args and extended_args walked as JSON and normalised
again as Action Scheduler stores them; log messages scrubbed.

Why it matters: Each rule has a positive and a negative fixture; rhythm-roadie
profiles action-scheduler lists every table, column and rule with what it was
checked against.

What Roadie does: copy and prepare apply these rules when the profile is
detected or named in profiles:.

By hand: Override a rule under columns: in .roadie/config.yaml, or leave a
table out with drop: or truncate:.

