# info.one-pass: the name index was skipped (info)

What: The run used --one-pass, so Roadie didn't build the index of the
source's names before it wrote the copy.

Why it matters: A name that sits in free text (a comment, a note, a form
message) is found only by the detectors, not by the index of names the
source holds, so some names in free text may stay. Site URLs, form fields
and every column rule still apply.

What Roadie does: Roadie still reads the source's site URLs and form fields
before it writes. From a stream it checks the site URLs after it writes and
stops if one was missed: a dump file is removed, while a database or site
target is left mixed and the message says how to copy again. It also notes
in the report that the name index was skipped.

By hand: Copy again without --one-pass when the copy must hold no name from
production in free text.
