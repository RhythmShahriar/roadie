# on_unparseable: a broken serialized value that holds personal text

What: A value that looks PHP serialized but doesn't parse and holds
personal-looking text, is blanked (pseudonymise.on_unparseable: blank, the
default) or stops the run (fail).

Why it matters: Roadie can't rewrite inside a value it can't read without
breaking it further; leaving it would keep the personal text.

What Roadie does: data.unparseable lists each one blanked.

By hand: Fix the value on production, or set on_unparseable: fail to stop
and look at each one.
