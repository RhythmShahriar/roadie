# config.points-at-production: the copy points at production services (critical)

What: DB_HOST, WP_REDIS_HOST, cache key salts or S3 offload settings equal
production's or match production:.

Why it matters: A copy that writes to production's database, cache or bucket
changes production.

What Roadie does: Roadie reports it and never guesses the staging's own hosts.

By hand: Point those settings at the staging's services by hand, then run
verify.

