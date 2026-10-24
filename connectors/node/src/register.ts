// Side-effect entry: `node --import @rhythmsh/echo/register app.js`,
// `node -r @rhythmsh/echo/register app.js` or `import '@rhythmsh/echo/register'`
// as the first import of the app.

import { register } from './index.js';

void register();
