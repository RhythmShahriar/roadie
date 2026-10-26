# prepare-confirm: prepare needs the target confirmed (exit 2)

What: prepare ran without a terminal and without --confirm-target, or the host
typed didn't match.

Why it matters: prepare rewrites the database a site is running on.

By hand: Pass --confirm-target <host> (the site's host or the database name).

