# action-scheduler-args: Action Scheduler's args written back as it stores them

What: After the args of a scheduled action (wp_actionscheduler_actions)
are pseudonymised, Roadie writes args and extended_args back the way
Action Scheduler stores them: JSON of up to 191 bytes in args, longer JSON
in extended_args with its md5 in args. A fake can push the JSON over 191
bytes or bring it back under.

Why it matters: JSON cut at 191 bytes, or an md5 that no longer matches,
leaves Action Scheduler unable to read the action's arguments.

What Roadie does: copy and prepare apply it to every action the
action-scheduler profile changes.

By hand: Nothing to do. To leave production's queued actions out of the
copy, add {prefix}actionscheduler_actions under truncate:.
