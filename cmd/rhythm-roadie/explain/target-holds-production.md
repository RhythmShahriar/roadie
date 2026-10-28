# target-holds-production: the staging may still call itself production (exit 3)

What: prepare found a siteurl on the staging and nothing says whether it is
the live site's URL: no --production-url, no production: and no --url that
matches it.

Why it matters: Rewriting URLs needs to know which one is live.

By hand: Pass --production-url <live URL> (never the staging's own URL), or
--url <siteurl> when the siteurl is the staging's own URL already. On a
terminal Roadie asks instead.

