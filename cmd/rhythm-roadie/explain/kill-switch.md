# kill-switch: a kill switch couldn't be installed (exit 1)

What: Roadie couldn't write one of its files, for example the Echo connector.

Why it matters: That side effect stays on.

By hand: Fix the folder's owner, then run rhythm-roadie prepare <staging>
--only side-effects.

