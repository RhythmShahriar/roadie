# known-values: a production value outside the covered columns gets its fake

What: An email, phone or URL that is a production person's own value (read
from the covered columns in the first pass) gets the same fake wherever else
it sits whole: a plugin's settings, a theme option, a form field or the key
of a JSON or serialized value.

Why it matters: Without it the person's real address survives in a column no
rule names; verify's exact check flags it.

What Roadie does: copy and prepare replace the whole value, keeping the
format around it (serialized lengths, JSON escapes).

By hand: Nothing to do. To cover the column by name, add it under columns:
in .roadie/config.yaml.
