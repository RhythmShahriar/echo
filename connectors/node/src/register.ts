// Side-effect entry: `node --import @rhythm/echo/register app.js`,
// `node -r @rhythm/echo/register app.js` or `import '@rhythm/echo/register'`
// as the first import of the app.

import { register } from './index.js';

void register();
