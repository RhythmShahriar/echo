import { withEcho } from '@rhythmsh/echo/next';

/** @type {import('next').NextConfig} */
const nextConfig = {
  // the fixture sits inside the package; keep Next.js from tracing the package's own lockfile
  outputFileTracingRoot: import.meta.dirname,
  turbopack: { root: import.meta.dirname },
};

export default withEcho(nextConfig);
