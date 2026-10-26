# config.salts: salts or APP_KEY equal production's (medium)

What: The WordPress salts or Laravel's APP_KEY are the same as production's,
or --keep-app-key was used.

Why it matters: Anyone with the copy can forge cookies that production
accepts.

What Roadie does: copy and prepare regenerate the eight salts and rotate
APP_KEY unless --keep-app-key.

By hand: Generate new salts (https://api.wordpress.org/secret-key/1.1/salt/)
or run php artisan key:generate.

