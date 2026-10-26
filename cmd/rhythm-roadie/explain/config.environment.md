# config.environment: WP_ENVIRONMENT_TYPE isn't staging (critical)

What: WP_ENVIRONMENT_TYPE is unset, production or defined after
wp-settings.php; for Laravel APP_ENV=production.

Why it matters: WordPress reads unset as production; the Echo seal and many
plugins only hold back when the environment says staging.

What Roadie does: Roadie defines WP_ENVIRONMENT_TYPE as staging in
wp-config.php, in place or before the stop-editing line.

By hand: Set define( 'WP_ENVIRONMENT_TYPE', 'staging' ); above the require of
wp-settings.php.

