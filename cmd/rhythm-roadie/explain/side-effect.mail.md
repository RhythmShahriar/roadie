# side-effect.mail: mail from the copy goes out for real (critical)

What: No Echo connector and no pre_wp_mail block, an SMTP or API mailer with
live credentials, or Echo's seal off or letting mail through.

Why it matters: A copy that sends mail emails real customers: order notes,
renewals, password resets.

What Roadie does: Roadie installs the Echo connector in seal mode, switches
SMTP plugins to the PHP mailer with their credentials blanked and blocks
wp_mail when the connector is gone.

By hand: Run rhythm echo up on the staging host, then rhythm-roadie prepare
<staging> --only side-effects.

