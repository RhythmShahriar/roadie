# search.indexable: search engines can index the copy (medium)

What: No Roadie noindex mu-plugin and blog_public = 1, a physical robots.txt
that allows crawling or, with --site, no noindex header.

Why it matters: Indexed staging pages leak data and compete with production in
search.

What Roadie does: Roadie adds noindex headers, a robots.txt filter and a
crawler block; blog_public in the database is left as it is.

By hand: Run rhythm-roadie prepare <staging> --only noindex; protect the copy
with basic auth too.

