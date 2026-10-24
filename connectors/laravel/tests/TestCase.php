<?php

namespace Rhythm\Echo\Tests;

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Mail\Transport\ArrayTransport;
use Illuminate\Notifications\SlackChannelServiceProvider;
use Illuminate\Notifications\VonageChannelServiceProvider;
use Illuminate\Support\Facades\Schema;
use Orchestra\Testbench\TestCase as Testbench;
use Rhythm\Echo\EchoServiceProvider;
use Rhythm\Echo\Mail\EchoTransport;
use RuntimeException;

/**
 * Boots a Testbench app with the connector pointed at a fake Echo (PHP's
 * built-in server running tests/Fixtures/echo-server.php).
 */
abstract class TestCase extends Testbench
{
    public const PORT = 18951;

    public const DEAD = 'http://127.0.0.1:18999';

    /** @var resource|null */
    private static $server = null;

    protected static string $state = '';

    /** Overridden by tests that need another environment. */
    protected string $env = 'testing';

    /** @var array<string, mixed> */
    protected array $echoConfig = [];

    public static function setUpBeforeClass(): void
    {
        parent::setUpBeforeClass();
        if (self::$server !== null) {
            return;
        }
        self::$state = sys_get_temp_dir().'/rhythm-echo-test-'.getmypid();
        @mkdir(self::$state, 0777, true);
        $cmd = [PHP_BINARY, '-S', '127.0.0.1:'.self::PORT, __DIR__.'/Fixtures/echo-server.php'];
        self::$server = proc_open($cmd, [1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes,
            null, ['ECHO_STATE' => self::$state] + getenv()) ?: null;
        for ($i = 0; $i < 50; $i++) {
            if (@fsockopen('127.0.0.1', self::PORT)) {
                register_shutdown_function(fn () => self::$server && proc_terminate(self::$server));

                return;
            }
            usleep(100_000);
        }
        throw new RuntimeException('fake Echo did not start');
    }

    protected function setUp(): void
    {
        @unlink(self::$state.'/captures.jsonl');
        @unlink(self::$state.'/token');
        $this->mode('seal');
        putenv('RHYTHM_ECHO_RUN');
        unset($_ENV['RHYTHM_ECHO_RUN'], $_SERVER['RHYTHM_ECHO_RUN']);
        parent::setUp();
    }

    protected function getPackageProviders($app): array
    {
        return [EchoServiceProvider::class, VonageChannelServiceProvider::class, SlackChannelServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['env'] = $this->env;
        $app['config']->set('app.url', 'http://site.test');
        $app['config']->set('mail.default', 'array');
        $app['config']->set('mail.from', ['address' => 'shop@site.test', 'name' => 'Site']);
        $app['config']->set('rhythm-echo', array_merge($app['config']->get('rhythm-echo', []), [
            'enabled' => true,
            'url' => 'http://127.0.0.1:'.self::PORT,
            'timeout' => 2.0,
        ], $this->echoConfig));
        $app['config']->set('database.default', 'testing');
        $app['config']->set('queue.default', 'database');
        $app['config']->set('queue.connections.database', ['driver' => 'database', 'table' => 'jobs', 'queue' => 'default', 'retry_after' => 90, 'connection' => 'testing']);
        $app['view']->addNamespace('echo-test', __DIR__.'/Fixtures/views');
        $app['config']->set('services.slack.notifications', ['bot_user_oauth_token' => 'xox'.'b-fake', 'channel' => '#orders']);
        $app['config']->set('vonage', ['api_key' => 'fake-key', 'api_secret' => 'fake-secret-0000', 'sms_from' => '15555550000']);
    }

    protected function createJobsTable(): void
    {
        Schema::create('jobs', function (Blueprint $t) {
            $t->id();
            $t->string('queue')->index();
            $t->longText('payload');
            $t->unsignedTinyInteger('attempts');
            $t->unsignedInteger('reserved_at')->nullable();
            $t->unsignedInteger('available_at');
            $t->unsignedInteger('created_at');
        });
    }

    protected function mode(string $mode): void
    {
        file_put_contents(self::$state.'/mode', $mode);
    }

    protected function requireToken(string $token): void
    {
        file_put_contents(self::$state.'/token', $token);
    }

    /**
     * @return list<array<string, mixed>>
     */
    protected function captures(?string $kind = null): array
    {
        $file = self::$state.'/captures.jsonl';
        $out = [];
        foreach (is_file($file) ? file($file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [] : [] as $line) {
            $c = json_decode($line, true);
            if (is_array($c) && ($kind === null || $c['kind'] === $kind)) {
                $out[] = $c;
            }
        }

        return $out;
    }

    /**
     * Messages the array transport (the "real" mailer here) received,
     * whether or not the connector wrapped it.
     */
    protected function delivered(): int
    {
        $transport = $this->app->make('mail.manager')->mailer('array')->getSymfonyTransport();
        if ($transport instanceof EchoTransport) {
            $transport = $transport->inner();
        }
        $this->assertInstanceOf(ArrayTransport::class, $transport);

        return count($transport->messages());
    }
}
