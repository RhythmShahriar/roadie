# soundcheck.failed: Soundcheck found problems on the copy (medium)

What: copy --soundcheck ran rhythm-soundcheck run <url> on the copy and it
exited 1.

Why it matters: The copy may not work as a staging site even though its data
is pseudonymised.

What Roadie does: Roadie reports Soundcheck's counts; it never changes the
site for them.

By hand: Run rhythm soundcheck run <url> and look at its report.

