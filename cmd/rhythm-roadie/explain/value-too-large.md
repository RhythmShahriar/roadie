# value-too-large: a value is too large (exit 3)

What: A single value is over --max-value (256 MiB by default).

Why it matters: Roadie holds one value in memory at a time; the cap keeps
memory flat.

By hand: Pass --max-value 1GiB if the value is real.

