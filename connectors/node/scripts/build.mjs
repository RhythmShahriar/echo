// Builds dist/esm and dist/cjs with tsc alone. TypeScript decides a file's
// output format from the nearest package.json "type" (module: nodenext), so
// the CJS build compiles a copy of src/ that sits under a "commonjs"
// package.json. That keeps real dynamic import() in the CJS output, which
// Nodemailer's ESM build and next/headers need.

import { execFileSync } from 'node:child_process';
import { cpSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const tsc = join(root, 'node_modules', 'typescript', 'bin', 'tsc');
const run = (...args) => execFileSync(process.execPath, [tsc, ...args], { cwd: root, stdio: 'inherit' });

rmSync(join(root, 'dist'), { recursive: true, force: true });
rmSync(join(root, '.build'), { recursive: true, force: true });

run('-p', 'tsconfig.build.json');
writeFileSync(join(root, 'dist/esm/package.json'), '{ "type": "module" }\n');

const cjsSrc = join(root, '.build', 'cjs', 'src');
mkdirSync(cjsSrc, { recursive: true });
cpSync(join(root, 'src'), cjsSrc, { recursive: true });
writeFileSync(join(root, '.build', 'cjs', 'package.json'), '{ "type": "commonjs" }\n');
writeFileSync(
  join(root, '.build', 'cjs', 'tsconfig.json'),
  JSON.stringify({
    extends: '../../tsconfig.build.json',
    compilerOptions: { rootDir: 'src', outDir: '../../dist/cjs', types: ['node'], typeRoots: ['../../node_modules/@types'] },
    include: ['src'],
  }),
);
run('-p', '.build/cjs/tsconfig.json');
writeFileSync(join(root, 'dist/cjs/package.json'), '{ "type": "commonjs" }\n');
rmSync(join(root, '.build'), { recursive: true, force: true });
console.log('built dist/esm and dist/cjs');
