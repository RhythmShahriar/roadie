# side-effect.connector-changed: the Echo connector was changed (high)

What: wp-content/mu-plugins/rhythm-echo.php differs from the connector Roadie
installs.

Why it matters: A changed connector may not seal mail any more.

What Roadie does: prepare installs the embedded connector again.

By hand: Run rhythm-roadie prepare <staging> --only side-effects.

