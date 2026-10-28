# interrupted: the run was stopped by Ctrl-C or a signal (exit 130)

What: The run got Ctrl-C or a signal (SIGINT or SIGTERM) and stopped before
it finished.

Why it matters: The stop box says the state the target was left in. An
untouched target is as it was before the run; a target in a mixed state holds
part of the copy and shouldn't be used as it is.

By hand: Run the command again. If the box says the target is in a mixed
state, restore the backup the run named or run the copy again before you use
the target.
