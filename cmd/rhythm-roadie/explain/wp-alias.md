# wp-alias: a WP-CLI alias Roadie can't use (exit 2)

What: The site was named as a WP-CLI alias (@prod) and Roadie can't turn it
into a site: no wp-cli.yml or wp-cli.local.yml from the working folder up
holds it, or its ssh value is not user@host[:port]/path (a docker, vagrant or
other non-ssh alias), or it names no absolute site folder.

Why it matters: Roadie opens an alias only as an ssh site or a local folder.
Nothing was read or written; the target is untouched.

By hand: Add the alias to wp-cli.yml in the project folder, such as

    @prod:
      ssh: deploy@shop.example.com/var/www/shop

or name the site directly as ssh://user@host/path, or as a local folder.
