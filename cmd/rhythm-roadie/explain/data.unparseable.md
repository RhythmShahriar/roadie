# data.unparseable: a serialized value that doesn't parse (medium)

What: A value that looks like PHP serialized data but doesn't parse while it
holds text shaped like personal data.

Why it matters: Roadie can't rewrite inside a value it can't parse without
breaking it, so it can't pseudonymise what is inside either.

What Roadie does: With on_unparseable: blank (the default) the value is
blanked; with fail the copy stops.

By hand: Look at the plugin that wrote the value; a truncated or hand-edited
option is the usual cause.

