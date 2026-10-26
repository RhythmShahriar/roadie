# urls: production URLs rewritten to the staging URL

What: Every production URL in the copy (siteurl, home, links in posts,
options, meta and serialized or JSON values) is rewritten to the staging
URL, with serialized lengths fixed. data.changes counts the values it
changed in each column.

Why it matters: A copy that still links to production sends staff and
crawlers to the live shop; a login or a form there acts on real data.

What Roadie does: copy and prepare rewrite production's hosts, read from the
source (siteurl, home, APP_URL) and production:, to --url. A blog of a
multisite network goes to its --url-map entry.

By hand: Add a host Roadie missed under production: in .roadie/config.yaml,
or map it with --url-map <host>=<staging host>.
