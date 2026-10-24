<?php
/**
 * Plugin Name: Early mailer (test)
 * Description: For the Rhythm Echo Docker test: when the request asks for it, defines wp_mail() before Echo loads, the way an SMTP plugin installed as a mu-plugin would. Like FluentSMTP's, it fires pre_wp_mail and phpmailer_init.
 */

// Declared inside the if: PHP binds top-level functions before the file
// runs, so a return above them would not stop the declaration.
if ( defined( 'ABSPATH' ) && isset( $_SERVER['HTTP_X_TEST_EARLY_MAILER'] ) ) {
function wp_mail( $to, $subject, $message, $headers = '', $attachments = array() ) {
	$atts = apply_filters( 'wp_mail', compact( 'to', 'subject', 'message', 'headers', 'attachments' ) );
	$pre  = apply_filters( 'pre_wp_mail', null, $atts );
	if ( null !== $pre ) {
		return $pre;
	}
	global $phpmailer;
	require_once ABSPATH . WPINC . '/PHPMailer/PHPMailer.php';
	require_once ABSPATH . WPINC . '/PHPMailer/SMTP.php';
	require_once ABSPATH . WPINC . '/PHPMailer/Exception.php';
	$phpmailer = new PHPMailer\PHPMailer\PHPMailer( true );
	$phpmailer->setFrom( 'early@shop.test' );
	$phpmailer->addAddress( is_array( $to ) ? $to[0] : $to );
	$phpmailer->Subject = $subject;
	$phpmailer->Body    = $message;
	do_action_ref_array( 'phpmailer_init', array( &$phpmailer ) );
	try {
		return $phpmailer->send();
	} catch ( PHPMailer\PHPMailer\Exception $e ) {
		return false;
	}
}
}
