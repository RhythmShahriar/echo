import { defineConfig } from 'vitest/config';

// Safety net: test workers start with every outbound request routed to a
// dead proxy (Node >= 22.21 / 24.5 honour NODE_USE_ENV_PROXY for fetch,
// http and https). If a hook ever let a request through to a real provider
// host, it fails with ECONNREFUSED instead of reaching the internet.
// Loopback stays direct for the fake Echo and fake APIs.
process.env.NODE_USE_ENV_PROXY = '1';
process.env.HTTP_PROXY = process.env.HTTPS_PROXY = 'http://127.0.0.1:9';
process.env.NO_PROXY = '127.0.0.1,localhost,::1';

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    pool: 'forks',
    testTimeout: 15000,
  },
});
