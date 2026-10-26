<?php
define('DB_NAME', 'shop');
define('DB_USER', 'shop');
define('DB_PASSWORD', 'fixture-pass-4');
define('DB_HOST', 'localhost');
$table_prefix = 'wp_';
if ( !defined('ABSPATH') )
	define('ABSPATH', dirname(__FILE__) . '/');
require_once(ABSPATH . 'wp-settings.php');
