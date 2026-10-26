# uncovered-values: personal values no rule covers get fakes

What: A leaf of a serialized or JSON value no rule covers gets fakes for
what it plainly holds: a phone, name, street or postcode by its key (phone,
first_name, address_1) and anywhere a personal email, a strong-format phone,
a public IP address or a card number. A key of the value that holds an email
or a phone gets a fake too.

Why it matters: Plugins keep customer data in their own settings and cron
arguments, where no profile rule reaches it.

What Roadie does: copy and prepare replace those parts and keep the rest of
the value as it was. A street in an options or settings table is the
business's own and stays.

By hand: Add the column under columns: in .roadie/config.yaml to choose the
rule yourself, or keep an address with verify.allow_emails.
