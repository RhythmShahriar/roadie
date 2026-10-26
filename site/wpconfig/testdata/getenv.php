<?php
define('DB_NAME', getenv('DB_NAME') ?: 'shop');
define('DB_USER', $_ENV['DB_USER']);
define('DB_PASSWORD', getenv('DB_PASS'));
define('DB_HOST', $_SERVER['DB_HOST'] ?? "localhost");
define("WP_HOME", "https://{$_SERVER['HTTP_HOST']}");
define('WP_CACHE_KEY_SALT', DB_NAME . ':cache');
$table_prefix = getenv('PREFIX');

require_once ABSPATH . 'wp-settings.php';
