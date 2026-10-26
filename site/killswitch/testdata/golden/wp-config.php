<?php
define( 'DB_NAME', 'shop' );
define( 'DB_USER', 'shop' );
define( 'DB_PASSWORD', 'x' );
define( 'DB_HOST', 'localhost' );
define( 'WP_HOME', 'https://staging.shop.test' );
define( 'STRIPE_SECRET_KEY', '' );
define( 'AUTH_KEY',         'fixture-salt-01-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'SECURE_AUTH_KEY',  'fixture-salt-02-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'LOGGED_IN_KEY',    'fixture-salt-03-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'NONCE_KEY',        'fixture-salt-04-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'AUTH_SALT',        'fixture-salt-05-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'SECURE_AUTH_SALT', 'fixture-salt-06-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'LOGGED_IN_SALT',   'fixture-salt-07-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
define( 'NONCE_SALT',       'fixture-salt-08-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' );
$table_prefix = 'wp_';
define( 'WP_ENVIRONMENT_TYPE', 'staging' );
define( 'DISABLE_WP_CRON', true );
/* That's all, stop editing! Happy publishing. */
require_once ABSPATH . 'wp-settings.php';
