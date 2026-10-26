<?php
define( "DB_NAME", "shop" );
define( "DB_USER", "shop" );
define( "DB_PASSWORD", "fixture\$pass\"3" );
define( "DB_HOST", "db.internal:3306" );
$table_prefix = 'wp_';
define( "WP_ENVIRONMENT_TYPE" , "production" );
define( 'DISABLE_WP_CRON', FALSE );

/* That's all, stop editing! Happy publishing. */
require_once( ABSPATH . 'wp-settings.php' );
