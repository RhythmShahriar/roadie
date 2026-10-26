<?php
define('DB_NAME', 'shop');
define('DB_USER', 'shop');
define('DB_PASSWORD', 'fixture-pass-2');
define('DB_HOST', 'localhost');
$table_prefix  = "shop_";

if ( isset( $_SERVER['HTTP_HOST'] ) && 'shop.test' === $_SERVER['HTTP_HOST'] ) {
	define( 'WP_ENVIRONMENT_TYPE', 'production' );
} else {
	define( 'WP_DEBUG', true );
}
if ( file_exists( __DIR__ . '/local.php' ) ) define( 'DISABLE_WP_CRON', false );
defined( 'WP_CACHE' ) or define( 'WP_CACHE', true );
if ( ! defined( 'FS_METHOD' ) ) :
	define( 'FS_METHOD', 'direct' );
endif;

/* That's all, stop editing! */
require_once ABSPATH . 'wp-settings.php';
