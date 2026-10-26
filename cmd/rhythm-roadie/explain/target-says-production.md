# target-says-production: the target says it is production (exit 3)

What: The target's WP_ENVIRONMENT_TYPE is unset or production, or
APP_ENV=production.

Why it matters: WordPress reads an unset environment as production, so Roadie
can't tell this target apart from production.

By hand: Set WP_ENVIRONMENT_TYPE to staging on the target, or pass
--target-is-staging <host> naming its host.

