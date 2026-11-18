// Check generated types against the current document without relying on HEAD.
// This also works while preparing the first commit of a new extension.
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const scratch = mkdtempSync(join(tmpdir(), 'octoeverywhere-client-'));
try {
  const output = join(scratch, 'module-api.ts');
  execFileSync(process.execPath, [
    join(root, 'client/node_modules/openapi-typescript/bin/cli.js'),
    join(root, 'client/openapi.json'), '-o', output,
  ], { cwd: root, stdio: 'inherit' });
  const actual = readFileSync(join(root, 'client/src/generated/module-api.ts'));
  if (!actual.equals(readFileSync(output))) {
    throw new Error('Generated client types are stale. Run make client or scripts/build.ps1 Client.');
  }
} finally {
  // Verify the unique temporary target before recursively removing it.
  if (dirname(resolve(scratch)) !== resolve(tmpdir()) || !basename(scratch).startsWith('octoeverywhere-client-')) {
    throw new Error('Refusing to remove a directory outside the generated-client temporary area.');
  }
  rmSync(scratch, { recursive: true, force: true });
}
