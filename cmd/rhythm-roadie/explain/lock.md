# lock: another run holds the target (exit 3)

What: Another Roadie run is copying to the same target.

Why it matters: Two runs on one target would mix their results.

By hand: Wait for it, or remove the lock file named in the message if that run
crashed.

