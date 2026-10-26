# Contributing to Roadie

Setup, commit messages and the review process are shared by every Rhythm tool and live in the
[shared CONTRIBUTING](https://github.com/RhythmShahriar/.github/blob/master/CONTRIBUTING.md).
This page covers what is particular to Roadie.

```sh
make hooks   # Conventional Commits check
make check   # lint + tests (the PHP oracle tests need php on PATH)
```

## Adding a profile rule

Profiles are YAML files in `profile/builtin/`, one per stack or plugin. A rule names the table,
the column (and a `where` or a path inside serialized or JSON data when it needs one) and what
happens to the value. Every rule needs a positive fixture (a row the rule changes) and a negative
fixture (a row it must leave alone); a table test fails when either is missing.

## Adding a detector

A detector implements `core.Detector` (`ID()` and a linear-time `Scan`) and reports only IDs
from `core/ids.go`. Its fixtures go in `detect/testdata/`: positives it must flag and negatives
it must leave alone, such as reserved domains and fictional phone numbers. Values reach output
only through the `core.Mask*` functions.

## Fake keys in tests

GitHub push protection blocks anything shaped like a live key, so tests never hold one as a
literal. Build fake keys at runtime with `testutil.J`, for example `testutil.J("sk_", "live_",
strings.Repeat("x", 24))`.
