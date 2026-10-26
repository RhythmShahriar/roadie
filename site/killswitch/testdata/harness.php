<?php
// Loads the Roadie mu-plugin with stand-ins for the WordPress functions it
// calls and prints the hooks it registered, as JSON. Usage:
// php harness.php <plugin> <environment type> <echo connector present: 0|1>
define( 'ABSPATH', __DIR__ . '/' );
$GLOBALS['hooks'] = array();
function add_filter( $name, $cb, $prio = 10 ) {
	$GLOBALS['hooks'][] = array( 'add', $name, $prio, is_string( $cb ) ? $cb : 'closure' );
	if ( 'init' === $name ) {
		$cb(); // run init now, so the queue runner removal is seen
	}
}
function add_action( $name, $cb, $prio = 10 ) {
	add_filter( $name, $cb, $prio );
}
function remove_action( $name, $cb ) {
	$GLOBALS['hooks'][] = array( 'remove', $name, 0, get_class( $cb[0] ) . '::' . $cb[1] );
}
function wp_get_environment_type() {
	return $GLOBALS['argv'][2];
}
class ActionScheduler_QueueRunner {
	public static function instance() {
		return new self();
	}
}
$dir = sys_get_temp_dir() . '/roadie-harness-' . getmypid();
@mkdir( $dir );
copy( $argv[1], $dir . '/roadie-staging.php' );
if ( '1' === $argv[3] ) {
	file_put_contents( $dir . '/rhythm-echo.php', '<?php' );
}
include $dir . '/roadie-staging.php';
@unlink( $dir . '/rhythm-echo.php' );
unlink( $dir . '/roadie-staging.php' );
rmdir( $dir );
echo json_encode( $GLOBALS['hooks'] ), "\n";
