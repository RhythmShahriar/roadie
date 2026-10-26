<?php
// A hostile file: Roadie reads it as text and must never run it.
system( 'touch ' . __DIR__ . '/pwned' );
shell_exec( `touch pwned-too` );
define( 'DB_NAME', 'shop' );
define( 'DB_USER', 'shop' );
define( 'DB_PASSWORD', 'fixture-pass-7' );
define( 'DB_HOST', 'localhost' );
$table_prefix = 'wp_';
/* That's all, stop editing! */
require_once ABSPATH . 'wp-settings.php';
