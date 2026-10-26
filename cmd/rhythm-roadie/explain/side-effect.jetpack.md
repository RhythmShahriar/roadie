# side-effect.jetpack: Jetpack is connected (medium)

What: Jetpack is connected and not in offline mode.

Why it matters: A connected copy can post to social accounts, send stats and
clash with production's connection.

What Roadie does: Roadie's mu-plugin turns on jetpack_offline_mode and blanks
the tokens.

By hand: Disconnect Jetpack on the copy, or define JETPACK_DEV_DEBUG.

