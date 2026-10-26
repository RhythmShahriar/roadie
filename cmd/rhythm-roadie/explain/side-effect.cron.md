# side-effect.cron: WP-Cron still runs (high)

What: DISABLE_WP_CRON isn't true.

Why it matters: Cron runs scheduled posts, renewals, imports and mail on every
visit.

What Roadie does: Roadie defines DISABLE_WP_CRON as true; scheduled events are
kept, none run.

By hand: Set define( 'DISABLE_WP_CRON', true ); in wp-config.php.

