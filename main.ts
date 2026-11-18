import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(fileURLToPath(import.meta.url));
const server = spawn(join(root, 'forge'), [], { stdio: 'inherit', env: process.env });
server.once('error', error => { console.error(error.message); process.exitCode = 1; });
server.once('exit', (code, signal) => { process.exitCode = signal ? 1 : (code ?? 1); });
for (const signal of ['SIGTERM', 'SIGINT'] as const) process.once(signal, () => server.kill(signal));
