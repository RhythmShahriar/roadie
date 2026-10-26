# config: a problem in .roadie/config.yaml (exit 2)

What: An unknown key, a wrong type, a bad glob, a non-ROADIE_ variable or a
non-reserved email domain.

Why it matters: Roadie reads its config strictly so a typo never turns a rule
off.

By hand: Fix the line the message names; rhythm-roadie config check validates
the file.

