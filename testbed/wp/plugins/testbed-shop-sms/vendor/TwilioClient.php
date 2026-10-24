<?php
/**
 * A minimal Twilio client in the style of the Twilio PHP SDK's CurlClient:
 * raw cURL straight to api.twilio.com, outside the WordPress HTTP API.
 */
final class Testbed_Twilio_Client {

	/** @var string */
	private $sid;

	/** @var string */
	private $token;

	public function __construct( string $sid, string $token ) {
		$this->sid   = $sid;
		$this->token = $token;
	}

	/**
	 * Sends an SMS and returns the message resource.
	 *
	 * @return array<string, mixed>
	 */
	public function send( string $to, string $from, string $body ): array {
		$ch = curl_init( 'https://api.twilio.com/2010-04-01/Accounts/' . $this->sid . '/Messages.json' );
		curl_setopt_array(
			$ch,
			array(
				CURLOPT_POST           => true,
				CURLOPT_POSTFIELDS     => http_build_query( array( 'To' => $to, 'From' => $from, 'Body' => $body ) ),
				CURLOPT_USERPWD        => $this->sid . ':' . $this->token,
				CURLOPT_RETURNTRANSFER => true,
				CURLOPT_TIMEOUT        => 10,
				CURLOPT_HTTPHEADER     => array( 'User-Agent: twilio-php/8.3.0 (Linux x86_64) PHP/' . PHP_VERSION ),
			)
		);
		$out  = curl_exec( $ch );
		$code = (int) curl_getinfo( $ch, CURLINFO_RESPONSE_CODE );
		$err  = curl_error( $ch );
		curl_close( $ch );
		if ( false === $out || $code >= 400 ) {
			throw new RuntimeException( sprintf( 'Twilio: HTTP %d %s', $code, $err ) );
		}
		$data = json_decode( (string) $out, true );
		return is_array( $data ) ? $data : array();
	}
}
