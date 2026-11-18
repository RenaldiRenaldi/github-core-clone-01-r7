import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';

const root = dirname(fileURLToPath(import.meta.url));
const binary = join(tmpdir(), `r7-forge-${process.pid}`);
const build = spawn('go', ['build', '-o', binary, '.'], { cwd: root, stdio: 'inherit' });
build.on('exit', code => {
  if (code !== 0) process.exit(code ?? 1);
  const server = spawn(binary, [], { stdio: 'inherit', env: process.env });
  server.on('exit', status => process.exit(status ?? 1));
});
