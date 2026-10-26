# data.encrypted: an encrypted value no scanner can read (info)

What: A Laravel encrypted cast or a ShouldBeEncrypted job payload.

Why it matters: Roadie can't see inside it, so it can't say whether it holds
personal data. With a rotated APP_KEY the copy can't read it either.

What Roadie does: It is listed so the report says what it didn't check.

By hand: Truncate the table if the copy doesn't need it, or keep APP_KEY with
--keep-app-key and accept that the copy can read it.

