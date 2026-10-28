# dump-bomb: the dump grew too much while reading (exit 3)

What: The decompressed input grew past 20 times its compressed size (or
--max-output-ratio).

Why it matters: That is what a compression bomb does.

By hand: If the dump is real, pass --max-output-ratio 50 to copy, prepare,
plan or verify. A copy that stops here leaves the target untouched.

