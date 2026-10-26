# target-holds-production: the staging still calls itself production (exit 3)

What: prepare found the staging's siteurl equal to the production URL and
nothing says which is which.

Why it matters: Rewriting URLs needs to know which one is live.

By hand: Pass --production-url <live URL> or answer the question on a
terminal.

